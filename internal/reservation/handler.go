package reservation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"reservas-restaurante/internal/httpx"
)

// Duas interfaces, porque são duas responsabilidades diferentes: criar reserva
// passa pelo domínio (validação, heurística, retry); ler e cancelar vão direto
// ao repositório. O *Allocator satisfaz a primeira, o *PostgresRepo a segunda,
// e nenhum dos dois precisou declarar nada.
type allocator interface {
	CreateReservation(ctx context.Context, req AllocationRequest) (Reservation, error)
}

type repository interface {
	Get(ctx context.Context, id uuid.UUID) (Reservation, error)
	List(ctx context.Context, f ListFilter) ([]Reservation, error)
	Cancel(ctx context.Context, id uuid.UUID) error
}

type schedule interface {
	FreeWindows(ctx context.Context, tableID uuid.UUID, dia string) ([]Window, error)
	DayGrid(ctx context.Context, dia string) ([]TableAvailability, error)
}

type Handler struct {
	allocator allocator
	repo      repository
	schedule  schedule
}

func NewHandler(a allocator, repo repository, s schedule) *Handler {
	return &Handler{allocator: a, repo: repo, schedule: s}
}

type createRequest struct {
	// Ausente, null ou vazio → "escolha a mesa por mim" (heurística automática).
	// Uma mesa  → override manual.
	// Várias    → COMBINAÇÃO: o staff empurrou as mesas, o sistema registra e
	//             protege cada uma delas com a EXCLUDE (Fase 3a).
	//
	// Era `table_id` (uuid único) até a Fase 3a. Mudança quebrada de contrato,
	// feita agora porque o frontend ainda não existe — depois dele, custaria o
	// dobro.
	TableIDs []uuid.UUID `json:"table_ids"`

	CustomerName  string    `json:"customer_name"  example:"Maria Silva"`
	CustomerPhone string    `json:"customer_phone" example:"11999998888"`
	PartySize     int       `json:"party_size"     example:"4"`
	StartsAt      time.Time `json:"starts_at"      example:"2026-08-01T19:00:00-03:00"`
	EndsAt        time.Time `json:"ends_at"        example:"2026-08-01T21:00:00-03:00"`
}

// responder concentra a tradução erro de domínio → status HTTP. Está numa função
// só para que os quatro handlers não divirjam, e para que o invariante do
// ErrSlotTaken tenha UM lugar onde ser vigiado.
func responder(w http.ResponseWriter, r *http.Request, err error, contexto string) {
	var ve ValidationError

	switch {
	case errors.As(err, &ve):
		httpx.Error(w, http.StatusBadRequest, ve.Message)

	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "Reserva não encontrada.")

	case errors.Is(err, ErrTableUnavailable):
		httpx.Error(w, http.StatusConflict, ErrTableUnavailable.Error()+".")

	case errors.Is(err, ErrNoAvailability):
		httpx.Error(w, http.StatusConflict, ErrNoAvailability.Error()+".")

	case errors.Is(err, ErrSlotTaken):
		// ErrSlotTaken é sinal INTERNO entre repositório e allocator. Chegar
		// aqui significa que algum caminho do CreateReservation esqueceu de
		// tratá-lo — é bug meu, não erro do usuário. Logamos alto e devolvemos
		// 500, em vez de mascarar como 409 e nunca descobrir.
		slog.Error("INVARIANTE VIOLADO: ErrSlotTaken vazou do allocator",
			"contexto", contexto, "rota", r.URL.Path)
		httpx.Error(w, http.StatusInternalServerError, "Erro interno.")

	default:
		slog.Error(contexto, "erro", err, "rota", r.URL.Path)
		httpx.Error(w, http.StatusInternalServerError, "Erro interno.")
	}
}

// Create godoc
//
//	@Summary		Cria uma reserva
//	@Description	`table_ids` vazio ou omitido: o sistema aloca automaticamente a menor mesa livre que comporte o grupo. Com uma mesa: usa exatamente aquela. Com várias: registra uma COMBINAÇÃO (o staff empurrou as mesas), validando a capacidade somada e protegendo cada mesa individualmente contra sobreposição.
//	@Tags			reservas
//	@Accept			json
//	@Produce		json
//	@Param			reserva	body		createRequest	true	"Dados da reserva"
//	@Success		201		{object}	Reservation
//	@Failure		400		{object}	httpx.ErrorResponse	"Dados inválidos, fora do expediente, mesa inativa/inexistente/repetida, ou grupo maior que a capacidade"
//	@Failure		409		{object}	httpx.ErrorResponse	"Mesa(s) pedida(s) ocupada(s), ou nenhuma mesa disponível no horário"
//	@Router			/reservations [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req createRequest

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "Corpo da requisição inválido.")
		return
	}

	// A conversão do tipo de transporte para o tipo de domínio. Seis linhas
	// explícitas no lugar do que em Java seria um MapStruct.
	res, err := h.allocator.CreateReservation(r.Context(), AllocationRequest{
		PreferredTableIDs: req.TableIDs,
		CustomerName:      req.CustomerName,
		CustomerPhone:     req.CustomerPhone,
		PartySize:         req.PartySize,
		StartsAt:          req.StartsAt,
		EndsAt:            req.EndsAt,
	})
	if err != nil {
		responder(w, r, err, "criando reserva")
		return
	}

	httpx.JSON(w, http.StatusCreated, res)
}

// List godoc
//
//	@Summary		Lista reservas
//	@Description	Filtros opcionais e combináveis. `date` é interpretado no fuso do restaurante, não em UTC.
//	@Tags			reservas
//	@Produce		json
//	@Param			date		query		string	false	"Dia da reserva, AAAA-MM-DD, no fuso do restaurante"	example(2026-08-01)
//	@Param			table_id	query		string	false	"Filtra por mesa (UUID)"
//	@Param			status		query		string	false	"confirmed ou cancelled"	Enums(confirmed, cancelled)
//	@Success		200			{array}		Reservation
//	@Failure		400			{object}	httpx.ErrorResponse	"Parâmetro de filtro inválido"
//	@Router			/reservations [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var f ListFilter

	if raw := q.Get("date"); raw != "" {
		// Valido o formato aqui, mas passo a STRING adiante. Converter para
		// time.Time em Go criaria um instante em algum fuso — e "qual dia?" já
		// estaria respondido, provavelmente errado. Quem sabe transformar um dia
		// em intervalo de instantes é o Postgres, com o AT TIME ZONE do fuso do
		// restaurante. O tipo mais burro é o mais correto aqui.
		if _, err := time.Parse(time.DateOnly, raw); err != nil {
			httpx.Error(w, http.StatusBadRequest, "Parâmetro 'date' deve estar no formato AAAA-MM-DD.")
			return
		}
		f.Date = &raw
	}

	if raw := q.Get("table_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "Parâmetro 'table_id' não é um UUID válido.")
			return
		}
		f.TableID = &id
	}

	if raw := q.Get("status"); raw != "" {
		// Aqui está o "muro" que o tipo nomeado Status não é: `Status(raw)`
		// compila para qualquer string, então a fronteira precisa conferir de
		// verdade. É a mesma razão de o CHECK existir no banco.
		s := Status(raw)
		if s != StatusConfirmed && s != StatusCancelled {
			httpx.Error(w, http.StatusBadRequest,
				"Parâmetro 'status' deve ser 'confirmed' ou 'cancelled'.")
			return
		}
		f.Status = &s
	}

	reservas, err := h.repo.List(r.Context(), f)
	if err != nil {
		responder(w, r, err, "listando reservas")
		return
	}

	httpx.JSON(w, http.StatusOK, reservas)
}

// Get godoc
//
//	@Summary	Detalha uma reserva
//	@Tags		reservas
//	@Produce	json
//	@Param		id	path		string	true	"ID da reserva (UUID)"
//	@Success	200	{object}	Reservation
//	@Failure	400	{object}	httpx.ErrorResponse	"ID não é um UUID"
//	@Failure	404	{object}	httpx.ErrorResponse	"Reserva não encontrada"
//	@Router		/reservations/{id} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "ID de reserva inválido.")
		return
	}

	res, err := h.repo.Get(r.Context(), id)
	if err != nil {
		responder(w, r, err, "buscando reserva")
		return
	}

	httpx.JSON(w, http.StatusOK, res)
}

// Delete godoc
//
//	@Summary		Cancela uma reserva
//	@Description	Soft delete: a linha permanece com status `cancelled` e o horário é liberado para reuso. Idempotente — cancelar duas vezes devolve 204.
//	@Tags			reservas
//	@Produce		json
//	@Param			id	path	string	true	"ID da reserva (UUID)"
//	@Success		204	"Cancelada"
//	@Failure		400	{object}	httpx.ErrorResponse	"ID não é um UUID"
//	@Failure		404	{object}	httpx.ErrorResponse	"Reserva não encontrada"
//	@Router			/reservations/{id} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "ID de reserva inválido.")
		return
	}

	if err := h.repo.Cancel(r.Context(), id); err != nil {
		responder(w, r, err, "cancelando reserva")
		return
	}

	// 204: cancelado, sem corpo. Devolver a reserva cancelada seria uma opção,
	// mas o cliente já sabe o que pediu e o único campo que mudou é o status.
	httpx.JSON(w, http.StatusNoContent, nil)
}

// Availability godoc
//
//	@Summary		Janelas livres de uma mesa no dia
//	@Description	Devolve os intervalos livres da mesa dentro do horário de funcionamento do restaurante, no fuso dele. Mesa inativa devolve lista vazia.
//	@Tags			mesas
//	@Produce		json
//	@Param			id		path		string	true	"ID da mesa (UUID)"
//	@Param			date	query		string	true	"Dia, AAAA-MM-DD, no fuso do restaurante"	example(2026-07-20)
//	@Success		200		{array}		Window
//	@Failure		400		{object}	httpx.ErrorResponse	"ID inválido, date ausente/malformado, ou mesa inexistente"
//	@Router			/tables/{id}/availability [get]
func (h *Handler) Availability(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "ID de mesa inválido.")
		return
	}

	// Obrigatório, ao contrário dos filtros do List: "janelas livres" sem dia é
	// uma pergunta sem resposta, não uma pergunta sem filtro.
	dia := r.URL.Query().Get("date")
	if dia == "" {
		httpx.Error(w, http.StatusBadRequest, "Parâmetro 'date' é obrigatório (AAAA-MM-DD).")
		return
	}

	janelas, err := h.schedule.FreeWindows(r.Context(), id, dia)
	if err != nil {
		responder(w, r, err, "consultando disponibilidade")
		return
	}

	httpx.JSON(w, http.StatusOK, janelas)
}

// DayAvailability godoc
//
//	@Summary		Grade de disponibilidade do dia (todas as mesas)
//	@Description	Devolve, para cada mesa ativa, as janelas livres no dia dentro do horário de funcionamento. É a resposta para "quais mesas posso combinar às 20h?" — o cliente filtra a grade por contenção de intervalo, sem refazer o cálculo. Mesas inativas não aparecem.
//	@Tags			mesas
//	@Produce		json
//	@Param			date	query		string	true	"Dia, AAAA-MM-DD, no fuso do restaurante"	example(2026-07-20)
//	@Success		200		{array}		TableAvailability
//	@Failure		400		{object}	httpx.ErrorResponse	"date ausente ou malformado"
//	@Router			/availability [get]
func (h *Handler) DayAvailability(w http.ResponseWriter, r *http.Request) {
	// Mesma obrigatoriedade do Availability, pela mesma razão: "janelas livres"
	// sem dia é pergunta sem resposta, não pergunta sem filtro. O formato em si
	// quem valida é o expedienteDe, no domínio — o handler não duplica o
	// time.Parse só para chegar à mesma conclusão.
	dia := r.URL.Query().Get("date")
	if dia == "" {
		httpx.Error(w, http.StatusBadRequest, "Parâmetro 'date' é obrigatório (AAAA-MM-DD).")
		return
	}

	grade, err := h.schedule.DayGrid(r.Context(), dia)
	if err != nil {
		responder(w, r, err, "consultando a grade do dia")
		return
	}

	httpx.JSON(w, http.StatusOK, grade)
}
