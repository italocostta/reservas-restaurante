package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"reservas-restaurante/internal/httpx"
	"reservas-restaurante/internal/reservation"
)

// repository é a interface que o handler consome — declarada aqui, satisfeita pelo
// *PostgresRepo. Só os métodos que o HTTP usa.
type repository interface {
	Load(ctx context.Context) (Settings, error)
	Save(ctx context.Context, s Settings) error
	ListExcecoes(ctx context.Context) ([]Exception, error)
	SaveExcecao(ctx context.Context, ex Exception) error
	DeleteExcecao(ctx context.Context, dia string) error
}

// agenda é a única coisa que este pacote sabe sobre reservas, e repare que ele NÃO
// importa reservation por causa dela: declara a PERGUNTA de que precisa e deixa o
// main.go escolher quem responde. É a mesma assimetria do table/handler.go (seção
// 15), atravessando a fronteira no sentido settings → reservation.
//
// A pergunta: "encolher o expediente para esta janela candidata deixaria quantas
// reservas confirmed fora do horário?". Quem responde, sem nunca ter ouvido falar
// desta interface, é o *reservation.PostgresRepo.
type agenda interface {
	// Devolve quantas reservas confirmed (com efeito futuro) cairiam FORA do
	// expediente candidato — dia fechado ou hora de início fora de [start, end) — e
	// a data/hora da mais próxima (DD/MM/YYYY HHhMM no fuso candidato; vazia se zero).
	ContarReservasForaDoExpediente(ctx context.Context, novo reservation.ServiceHours, diasAbertos []int) (int, string, error)

	// Devolve quantas reservas confirmed (com efeito futuro) caem num dia específico
	// (AAAA-MM-DD, dia de parede do restaurante) e a data/hora da mais próxima. É o
	// que o POST /service-exceptions consulta antes de FECHAR uma data.
	ContarReservasNoDia(ctx context.Context, dia string) (int, string, error)
}

type Handler struct {
	repo   repository
	agenda agenda
}

func NewHandler(repo repository, ag agenda) *Handler {
	return &Handler{repo: repo, agenda: ag}
}

// SettingsResponse é o expediente do jeito que o frontend lê: horário como string,
// dias da semana como lista de inteiros (0=domingo … 6=sábado), e as exceções.
//
// Substitui o antigo GET /service-hours que vivia no httpserver e lia a Config.
// Agora tem mais campos (dias e exceções) e a fonte é o banco.
type SettingsResponse struct {
	Start        string      `json:"start"         example:"18:00"`
	End          string      `json:"end"           example:"23:00"`
	TZ           string      `json:"tz"            example:"America/Sao_Paulo"`
	OpenWeekdays []int       `json:"open_weekdays" example:"0,1,2,3,4,5,6"`
	Exceptions   []Exception `json:"exceptions"`
}

// updateRequest é o corpo do PUT. Sem exceções aqui: elas têm endpoints próprios,
// porque adicionar um feriado não é a mesma operação que mudar o horário, e
// juntá-las obrigaria o frontend a reenviar a lista inteira de exceções a cada
// ajuste de horário.
type updateRequest struct {
	Start        string `json:"start"         example:"18:00"`
	End          string `json:"end"           example:"23:00"`
	TZ           string `json:"tz"            example:"America/Sao_Paulo"`
	OpenWeekdays []int  `json:"open_weekdays" example:"0,1,2,3,4,5,6"`
}

// Get godoc
//
//	@Summary		Expediente e dias de funcionamento do restaurante
//	@Description	Horário, fuso, dias da semana em que abre (0=domingo … 6=sábado) e as exceções por data. O frontend lê daqui em vez de guardar uma cópia.
//	@Tags			restaurante
//	@Produce		json
//	@Success		200	{object}	SettingsResponse
//	@Router			/service-hours [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	s, err := h.repo.Load(r.Context())
	if err != nil {
		slog.Error("lendo settings", "erro", err)
		httpx.Error(w, http.StatusInternalServerError, "Erro interno.")
		return
	}

	excecoes, err := h.repo.ListExcecoes(r.Context())
	if err != nil {
		slog.Error("listando exceções", "erro", err)
		httpx.Error(w, http.StatusInternalServerError, "Erro interno.")
		return
	}

	httpx.JSON(w, http.StatusOK, respostaDe(s, excecoes))
}

func respostaDe(s Settings, excecoes []Exception) SettingsResponse {
	dias := make([]int, 0, len(s.OpenWeekdays))
	for d := time.Sunday; d <= time.Saturday; d++ {
		if s.OpenWeekdays[d] {
			dias = append(dias, int(d))
		}
	}
	if excecoes == nil {
		excecoes = []Exception{}
	}
	return SettingsResponse{
		Start:        formatarDuracao(s.Hours.Start),
		End:          formatarDuracao(s.Hours.End),
		TZ:           s.Hours.TZ.String(),
		OpenWeekdays: dias,
		Exceptions:   excecoes,
	}
}

// Update godoc
//
//	@Summary		Altera o expediente e os dias de funcionamento
//	@Tags			restaurante
//	@Accept			json
//	@Produce		json
//	@Param			expediente	body		updateRequest	true	"Novo expediente"
//	@Success		200			{object}	SettingsResponse
//	@Failure		400			{object}	httpx.ErrorResponse	"Horário/fuso inválido, ou fim não é depois do início"
//	@Failure		409			{object}	httpx.ErrorResponse	"O novo expediente deixaria reservas confirmadas fora do horário"
//	@Router			/service-hours [put]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "Corpo da requisição inválido.")
		return
	}

	s, err := montarSettings(req)
	if err != nil {
		responder(w, err, "validando expediente")
		return
	}

	// ENCOLHER o expediente (tirar um dia de open_weekdays, ou apertar a janela de
	// horas) não pode passar por cima de uma reserva confirmed que já está marcada
	// exatamente no horário que deixaria de existir: a reserva continuaria de pé no
	// banco enquanto a agenda passaria a tratar aquele horário como fechado. É o
	// mesmo buraco da desativação de mesa (seção 15), do outro lado.
	//
	// Check-then-act, e portanto com corrida: uma reserva pode nascer entre a
	// contagem e o Save. Assim como a checagem da mesa, esta corrida NÃO tem
	// constraint no banco para ampará-la, e não vou fingir que tem — janela de
	// milissegundos numa operação que o staff faz raramente, contra dano recuperável
	// (reeditar o expediente). Aceito e registrado.
	//
	// Falha ao CONTAR vira 500, nunca 409: um erro de banco significa "não sei se
	// pode", e responder "não pode" seria mentir com confiança (seção 15).
	n, proxima, err := h.agenda.ContarReservasForaDoExpediente(r.Context(), s.Hours, diasAbertos(s.OpenWeekdays))
	if err != nil {
		slog.Error("contando reservas fora do expediente", "erro", err)
		httpx.Error(w, http.StatusInternalServerError, "Erro interno.")
		return
	}
	if n > 0 {
		httpx.Error(w, http.StatusConflict, fmt.Sprintf(
			"O novo expediente deixaria %s confirmada(s) fora do horário de funcionamento (a mais próxima em %s). Cancele-a(s) ou ajuste o expediente antes.",
			pluralReservas(n), proxima))
		return
	}

	if err := h.repo.Save(r.Context(), s); err != nil {
		// As constraints da 0009 (expediente_coerente, dias_validos) são a última
		// palavra. Se elas dispararem, é porque uma checagem de app deixou passar —
		// tratamos como erro interno, não como 400, para o bug aparecer no log.
		slog.Error("gravando settings", "erro", err)
		httpx.Error(w, http.StatusInternalServerError, "Erro interno.")
		return
	}

	excecoes, _ := h.repo.ListExcecoes(r.Context())
	httpx.JSON(w, http.StatusOK, respostaDe(s, excecoes))
}

// montarSettings valida e converte o corpo. As validações espelham as constraints
// do banco — mas existem aqui para dar mensagem amigável, não erro bruto (mesmo
// princípio da validação de reserva antes da EXCLUDE).
func montarSettings(req updateRequest) (Settings, error) {
	start, err := parseHHMM(req.Start)
	if err != nil {
		return Settings{}, reservation.ValidationError{Message: "Horário de abertura inválido (esperado HH:MM)."}
	}
	end, err := parseHHMM(req.End)
	if err != nil {
		return Settings{}, reservation.ValidationError{Message: "Horário de fechamento inválido (esperado HH:MM)."}
	}
	if end <= start {
		return Settings{}, reservation.ValidationError{Message: "O fechamento deve ser depois da abertura."}
	}

	tz, err := time.LoadLocation(req.TZ)
	if err != nil {
		return Settings{}, reservation.ValidationError{Message: "Fuso horário inválido."}
	}

	abertos := map[time.Weekday]bool{}
	for _, d := range req.OpenWeekdays {
		if d < 0 || d > 6 {
			return Settings{}, reservation.ValidationError{Message: "Dia da semana inválido (0=domingo … 6=sábado)."}
		}
		abertos[time.Weekday(d)] = true
	}
	if len(abertos) == 0 {
		return Settings{}, reservation.ValidationError{Message: "O restaurante precisa abrir em ao menos um dia da semana."}
	}

	return Settings{
		Hours:        reservation.ServiceHours{Start: start, End: end, TZ: tz},
		OpenWeekdays: abertos,
	}, nil
}

type excecaoRequest struct {
	Day    string `json:"day"     example:"2026-12-25"`
	IsOpen bool   `json:"is_open" example:"false"`
	Note   string `json:"note"    example:"Natal"`
}

// SaveException godoc
//
//	@Summary		Marca uma data como fechada ou aberta (exceção)
//	@Description	Define que uma data específica foge da regra semanal: fechada num dia normalmente aberto, ou aberta num dia normalmente fechado. Reenviar a mesma data sobrescreve.
//	@Tags			restaurante
//	@Accept			json
//	@Produce		json
//	@Param			excecao	body		excecaoRequest	true	"Data e se abre"
//	@Success		200		{object}	Exception
//	@Failure		400		{object}	httpx.ErrorResponse	"Data inválida (esperado AAAA-MM-DD)"
//	@Failure		409		{object}	httpx.ErrorResponse	"A data tem reservas confirmadas e não pode ser fechada"
//	@Router			/service-exceptions [post]
func (h *Handler) SaveException(w http.ResponseWriter, r *http.Request) {
	var req excecaoRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "Corpo da requisição inválido.")
		return
	}

	if _, err := time.Parse(time.DateOnly, req.Day); err != nil {
		httpx.Error(w, http.StatusBadRequest, "Data inválida (esperado AAAA-MM-DD).")
		return
	}

	// FECHAR uma data (is_open=false) por cima de reservas confirmed é o mesmo buraco
	// do PUT /service-hours e da desativação de mesa: a reserva fica de pé enquanto a
	// agenda passa a mostrar o dia como fechado. Só o FECHAMENTO é barrado — uma
	// abertura especial (is_open=true) só adiciona disponibilidade, nunca conflita,
	// do mesmo jeito que reativar mesa nunca consulta a agenda.
	//
	// Check-then-act com a corrida de sempre (uma reserva pode nascer entre a
	// contagem e o upsert), aceita pelos mesmos motivos. Falha ao contar vira 500,
	// nunca 409: erro de banco significa "não sei se pode".
	if !req.IsOpen {
		n, proxima, err := h.agenda.ContarReservasNoDia(r.Context(), req.Day)
		if err != nil {
			slog.Error("contando reservas no dia da exceção", "erro", err)
			httpx.Error(w, http.StatusInternalServerError, "Erro interno.")
			return
		}
		if n > 0 {
			httpx.Error(w, http.StatusConflict, fmt.Sprintf(
				"Esta data tem %s confirmada(s) (próxima em %s) e não pode ser fechada. Cancele-a(s) antes.",
				pluralReservas(n), proxima))
			return
		}
	}

	ex := Exception{Day: req.Day, IsOpen: req.IsOpen, Note: req.Note}
	if err := h.repo.SaveExcecao(r.Context(), ex); err != nil {
		slog.Error("gravando exceção", "erro", err)
		httpx.Error(w, http.StatusInternalServerError, "Erro interno.")
		return
	}

	httpx.JSON(w, http.StatusOK, ex)
}

// DeleteException godoc
//
//	@Summary		Remove uma exceção (a data volta a seguir a regra semanal)
//	@Tags			restaurante
//	@Produce		json
//	@Param			day	path	string	true	"Data da exceção (AAAA-MM-DD)"
//	@Success		204	"Removida"
//	@Failure		400	{object}	httpx.ErrorResponse	"Data inválida"
//	@Failure		409	{object}	httpx.ErrorResponse	"Remover a exceção re-fecharia o dia, que tem reservas confirmadas"
//	@Router			/service-exceptions/{day} [delete]
func (h *Handler) DeleteException(w http.ResponseWriter, r *http.Request) {
	dia := r.PathValue("day")
	if _, err := time.Parse(time.DateOnly, dia); err != nil {
		httpx.Error(w, http.StatusBadRequest, "Data inválida (esperado AAAA-MM-DD).")
		return
	}

	// Apagar uma exceção faz a data voltar à regra semanal. Se a data era uma
	// ABERTURA especial (is_open=true) num dia normalmente fechado, removê-la
	// RE-FECHA o dia — e as reservas criadas durante a abertura ficariam órfãs no dia
	// que a agenda passa a mostrar como fechado. É a terceira porta do mesmo gap do
	// PUT /service-hours (seção 16) e do POST (débito #17).
	//
	// A checagem só importa quando o dia fica FECHADO depois da remoção — ou seja,
	// quando o dia da semana NÃO está no open_weekdays. Se a regra semanal já abre
	// aquele dia, remover a exceção no máximo re-abre, e re-abrir nunca estranha
	// ninguém (mesmo princípio de reativar mesa e de is_open=true no POST).
	s, err := h.repo.Load(r.Context())
	if err != nil {
		slog.Error("lendo settings para checar remoção de exceção", "erro", err)
		httpx.Error(w, http.StatusInternalServerError, "Erro interno.")
		return
	}
	// O formato de `dia` já foi validado acima; o fuso do restaurante mantém a
	// convenção de dia de parede que o AbertoEm usa (embora, para uma data pura, o
	// dia da semana não dependa do fuso).
	d, _ := time.ParseInLocation(time.DateOnly, dia, s.Hours.TZ)
	if !s.OpenWeekdays[d.Weekday()] {
		n, proxima, err := h.agenda.ContarReservasNoDia(r.Context(), dia)
		if err != nil {
			slog.Error("contando reservas no dia da exceção a remover", "erro", err)
			httpx.Error(w, http.StatusInternalServerError, "Erro interno.")
			return
		}
		if n > 0 {
			httpx.Error(w, http.StatusConflict, fmt.Sprintf(
				"Esta data tem %s confirmada(s) (próxima em %s) e voltaria a ser um dia fechado ao remover a exceção. Cancele-a(s) antes.",
				pluralReservas(n), proxima))
			return
		}
	}

	if err := h.repo.DeleteExcecao(r.Context(), dia); err != nil {
		slog.Error("removendo exceção", "erro", err)
		httpx.Error(w, http.StatusInternalServerError, "Erro interno.")
		return
	}

	httpx.JSON(w, http.StatusNoContent, nil)
}

// responder traduz o ValidationError do domínio em 400. Reusa o tipo do pacote
// reservation em vez de criar um erro próprio — a mensagem amigável já é o que ele
// carrega.
func responder(w http.ResponseWriter, err error, contexto string) {
	var ve reservation.ValidationError
	if errors.As(err, &ve) {
		httpx.Error(w, http.StatusBadRequest, ve.Message)
		return
	}
	slog.Error(contexto, "erro", err)
	httpx.Error(w, http.StatusInternalServerError, "Erro interno.")
}

// diasAbertos achata o map de dias abertos na lista de DOW que a query espera (a
// mesma convenção 0=domingo do open_weekdays). O map é para consulta O(1); a query
// quer o conjunto.
func diasAbertos(m map[time.Weekday]bool) []int {
	dias := make([]int, 0, len(m))
	for d := time.Sunday; d <= time.Saturday; d++ {
		if m[d] {
			dias = append(dias, int(d))
		}
	}
	return dias
}

// pluralReservas evita "1 reservas" na mensagem lida pelo staff — mesmo cuidado do
// table/handler.go. Duplicado de propósito: importar table só por isto acoplaria
// dois domínios por uma função de uma linha.
func pluralReservas(n int) string {
	if n == 1 {
		return "1 reserva"
	}
	return fmt.Sprintf("%d reservas", n)
}

// parseHHMM converte "18:30" na duração desde a meia-noite. Duplicado do config de
// propósito: o config valida env no boot, este valida corpo HTTP em runtime, e são
// ciclos de vida diferentes que não devem compartilhar uma função que muda por um.
func parseHHMM(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, err
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}
