package settings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"reservas-restaurante/internal/reservation"
)

// repoStub satisfaz `repository` e registra se o Save foi tocado — o ponto do teste
// é justamente que o 409 barra ANTES do Save.
type repoStub struct {
	saves        int                   // chamadas a Save (PUT)
	excSaves     int                   // chamadas a SaveExcecao (POST exceção)
	deletes      int                   // chamadas a DeleteExcecao (DELETE exceção)
	openWeekdays map[time.Weekday]bool // nil → {segunda}
}

func (s *repoStub) Load(context.Context) (Settings, error) {
	tz, _ := time.LoadLocation("America/Sao_Paulo")
	dias := s.openWeekdays
	if dias == nil {
		dias = map[time.Weekday]bool{time.Monday: true}
	}
	return Settings{
		Hours:        reservation.ServiceHours{Start: 18 * time.Hour, End: 23 * time.Hour, TZ: tz},
		OpenWeekdays: dias,
	}, nil
}
func (s *repoStub) Save(context.Context, Settings) error              { s.saves++; return nil }
func (s *repoStub) ListExcecoes(context.Context) ([]Exception, error) { return nil, nil }
func (s *repoStub) SaveExcecao(context.Context, Exception) error      { s.excSaves++; return nil }
func (s *repoStub) DeleteExcecao(context.Context, string) error       { s.deletes++; return nil }

// agendaStub é o dublê configurável da contagem: n reservas fora, ou um erro ao
// contar. Registra as chamadas para o teste provar que a contagem foi consultada.
type agendaStub struct {
	n        int
	proxima  string
	err      error
	calls    int // chamadas a ContarReservasForaDoExpediente (PUT)
	diaCalls int // chamadas a ContarReservasNoDia (exceção)
}

func (a *agendaStub) ContarReservasForaDoExpediente(context.Context, reservation.ServiceHours, []int) (int, string, error) {
	a.calls++
	return a.n, a.proxima, a.err
}

// diaCalls conta separado: o teste de exceção precisa provar que a contagem POR DIA
// foi (ou não) consultada, sem confundir com a checagem do PUT.
func (a *agendaStub) ContarReservasNoDia(context.Context, string) (int, string, error) {
	a.diaCalls++
	return a.n, a.proxima, a.err
}

const corpoValido = `{"start":"19:00","end":"22:00","tz":"America/Sao_Paulo","open_weekdays":[1,2,3]}`

// Encolher o expediente por cima de uma reserva confirmed é o buraco da seção 16.
// O 409 tem que barrar ANTES do Save — senão o expediente já foi gravado e a
// reserva ficou órfã no horário que deixou de existir.
func TestUpdateNaoEncolheExpedienteComReservaFora(t *testing.T) {
	casos := []struct {
		nome       string
		agenda     *agendaStub
		wantStatus int
		wantContou int // a contagem foi consultada?
		wantSaves  int // o repositório chegou a gravar?
	}{
		{
			nome:       "reserva ficaria fora → 409, sem gravar",
			agenda:     &agendaStub{n: 2, proxima: "20/07/2026 20h00"},
			wantStatus: http.StatusConflict, wantContou: 1, wantSaves: 0,
		},
		{
			nome:       "nenhuma reserva fora → 200, grava",
			agenda:     &agendaStub{n: 0},
			wantStatus: http.StatusOK, wantContou: 1, wantSaves: 1,
		},
		{
			// Erro de banco na contagem significa "não sei se pode": responder 409
			// seria mentir com confiança. Vira 500, e NÃO grava.
			nome:       "falha ao contar → 500, sem gravar",
			agenda:     &agendaStub{err: errors.New("db caiu")},
			wantStatus: http.StatusInternalServerError, wantContou: 1, wantSaves: 0,
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			repo := &repoStub{}
			h := NewHandler(repo, tc.agenda)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/service-hours", strings.NewReader(corpoValido))
			h.Update(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, quero %d — corpo: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.agenda.calls != tc.wantContou {
				t.Errorf("agenda consultada %d vez(es), quero %d", tc.agenda.calls, tc.wantContou)
			}
			if repo.saves != tc.wantSaves {
				t.Errorf("Save chamado %d vez(es), quero %d — o 409/500 tem que barrar ANTES de gravar",
					repo.saves, tc.wantSaves)
			}
		})
	}
}

// A validação do corpo roda ANTES da contagem: um expediente malformado é 400 e
// nem consulta a agenda — nada a proteger num pedido que não vai persistir.
func TestUpdateCorpoInvalidoNaoConsultaAgenda(t *testing.T) {
	repo := &repoStub{}
	ag := &agendaStub{}
	h := NewHandler(repo, ag)

	rec := httptest.NewRecorder()
	// Fim antes do início: reprovado por montarSettings.
	req := httptest.NewRequest(http.MethodPut, "/service-hours",
		strings.NewReader(`{"start":"22:00","end":"19:00","tz":"America/Sao_Paulo","open_weekdays":[1]}`))
	h.Update(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, quero 400", rec.Code)
	}
	if ag.calls != 0 {
		t.Errorf("agenda consultada %d vez(es) num pedido inválido — quero 0", ag.calls)
	}
	if repo.saves != 0 {
		t.Errorf("Save chamado num pedido inválido — quero 0")
	}
}

// A mensagem do 409 é lida pelo staff: precisa dizer QUANTAS reservas e QUANDO é a
// mais próxima, não só "conflito".
func TestUpdateMensagemDo409DizQuantasEQuando(t *testing.T) {
	repo := &repoStub{}
	ag := &agendaStub{n: 1, proxima: "20/07/2026 20h00"}
	h := NewHandler(repo, ag)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/service-hours", strings.NewReader(corpoValido))
	h.Update(rec, req)

	corpo := rec.Body.String()
	if !strings.Contains(corpo, "1 reserva") {
		t.Errorf("mensagem = %s — precisa dizer QUANTAS reservas (e '1 reserva', não '1 reservas')", corpo)
	}
	if !strings.Contains(corpo, "20/07/2026 20h00") {
		t.Errorf("mensagem = %s — precisa dizer QUANDO é a mais próxima", corpo)
	}
}

// Fechar uma data com reserva confirmed em cima é o débito #17: a reserva ficaria de
// pé enquanto a agenda passaria a mostrar o dia como fechado. O 409 barra antes do
// upsert. E o caso que distingue esta checagem da do PUT: ABRIR (is_open=true) nunca
// consulta a agenda — só adiciona disponibilidade, como reativar mesa.
func TestSaveExceptionNaoFechaDataComReserva(t *testing.T) {
	casos := []struct {
		nome       string
		corpo      string
		agenda     *agendaStub
		wantStatus int
		wantContou int // ContarReservasNoDia foi chamada?
		wantSaves  int // a exceção chegou a ser gravada?
	}{
		{
			nome:       "fechar data com reserva → 409, sem gravar",
			corpo:      `{"day":"2026-12-25","is_open":false,"note":"Natal"}`,
			agenda:     &agendaStub{n: 2, proxima: "25/12/2026 20h00"},
			wantStatus: http.StatusConflict, wantContou: 1, wantSaves: 0,
		},
		{
			nome:       "fechar data livre → 200, grava",
			corpo:      `{"day":"2026-12-25","is_open":false,"note":"Natal"}`,
			agenda:     &agendaStub{n: 0},
			wantStatus: http.StatusOK, wantContou: 1, wantSaves: 1,
		},
		{
			// ABRIR não é fechar: é abertura especial num dia normalmente fechado. Se
			// este caso passar a chamar a contagem, alguém trocou `!req.IsOpen` por
			// algo que barra os dois — e aí abrir um dia viraria refém de reservas que
			// nem podiam existir num dia fechado.
			nome:       "ABRIR data (is_open=true) não consulta a agenda",
			corpo:      `{"day":"2026-12-25","is_open":true,"note":"evento"}`,
			agenda:     &agendaStub{n: 99},
			wantStatus: http.StatusOK, wantContou: 0, wantSaves: 1,
		},
		{
			// Erro de banco na contagem significa "não sei se pode": 500, não 409, e
			// não grava.
			nome:       "falha ao contar → 500, sem gravar",
			corpo:      `{"day":"2026-12-25","is_open":false}`,
			agenda:     &agendaStub{err: errors.New("db caiu")},
			wantStatus: http.StatusInternalServerError, wantContou: 1, wantSaves: 0,
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			repo := &repoStub{}
			h := NewHandler(repo, tc.agenda)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/service-exceptions", strings.NewReader(tc.corpo))
			h.SaveException(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, quero %d — corpo: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.agenda.diaCalls != tc.wantContou {
				t.Errorf("ContarReservasNoDia chamada %d vez(es), quero %d", tc.agenda.diaCalls, tc.wantContou)
			}
			if repo.excSaves != tc.wantSaves {
				t.Errorf("SaveExcecao chamado %d vez(es), quero %d — o 409/500 tem que barrar ANTES de gravar",
					repo.excSaves, tc.wantSaves)
			}
		})
	}
}

// A mensagem do 409 da exceção, como a do PUT, diz QUANTAS e QUANDO — o staff que a
// lê precisa saber o que cancelar antes.
func TestSaveExceptionMensagemDo409(t *testing.T) {
	repo := &repoStub{}
	ag := &agendaStub{n: 1, proxima: "25/12/2026 20h00"}
	h := NewHandler(repo, ag)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/service-exceptions",
		strings.NewReader(`{"day":"2026-12-25","is_open":false}`))
	h.SaveException(rec, req)

	corpo := rec.Body.String()
	if !strings.Contains(corpo, "1 reserva") {
		t.Errorf("mensagem = %s — precisa dizer QUANTAS reservas", corpo)
	}
	if !strings.Contains(corpo, "25/12/2026 20h00") {
		t.Errorf("mensagem = %s — precisa dizer QUANDO é a mais próxima", corpo)
	}
}

// weekdayDe devolve o dia da semana de uma data, no fuso do stub — para os casos do
// DELETE montarem open_weekdays que INCLUI ou EXCLUI exatamente o dia da data.
func weekdayDe(t *testing.T, dia string) time.Weekday {
	t.Helper()
	tz, _ := time.LoadLocation("America/Sao_Paulo")
	d, err := time.ParseInLocation(time.DateOnly, dia, tz)
	if err != nil {
		t.Fatal(err)
	}
	return d.Weekday()
}

// Remover uma abertura especial (is_open=true) num dia normalmente fechado re-fecha
// o dia e deixa órfãs as reservas criadas durante a abertura (débito #19). O 409
// barra antes do delete — mas SÓ quando o dia fica fechado; se a regra semanal já
// abre o dia, remover no máximo re-abre, e re-abrir nunca consulta a agenda.
func TestDeleteExceptionNaoReFechaDiaComReserva(t *testing.T) {
	const dia = "2026-12-25"
	wd := weekdayDe(t, dia)

	casos := []struct {
		nome       string
		aberto     map[time.Weekday]bool // regra semanal
		agenda     *agendaStub
		wantStatus int
		wantContou int // ContarReservasNoDia foi chamada?
		wantDelete int // a exceção chegou a ser removida?
	}{
		{
			nome:       "dia re-fecha + reserva → 409, sem remover",
			aberto:     map[time.Weekday]bool{}, // dia da data NÃO está aberto na semana
			agenda:     &agendaStub{n: 1, proxima: "25/12/2026 20h00"},
			wantStatus: http.StatusConflict, wantContou: 1, wantDelete: 0,
		},
		{
			// O caso que distingue este do fechamento direto: se a regra semanal já
			// abre o dia, remover a exceção não fecha nada — e nem consulta a agenda.
			nome:       "dia continua aberto na semana → 204, sem consultar agenda",
			aberto:     map[time.Weekday]bool{wd: true},
			agenda:     &agendaStub{n: 99},
			wantStatus: http.StatusNoContent, wantContou: 0, wantDelete: 1,
		},
		{
			nome:       "dia re-fecha, sem reserva → 204, remove",
			aberto:     map[time.Weekday]bool{},
			agenda:     &agendaStub{n: 0},
			wantStatus: http.StatusNoContent, wantContou: 1, wantDelete: 1,
		},
		{
			nome:       "falha ao contar → 500, sem remover",
			aberto:     map[time.Weekday]bool{},
			agenda:     &agendaStub{err: errors.New("db caiu")},
			wantStatus: http.StatusInternalServerError, wantContou: 1, wantDelete: 0,
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			repo := &repoStub{openWeekdays: tc.aberto}
			h := NewHandler(repo, tc.agenda)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/service-exceptions/"+dia, nil)
			req.SetPathValue("day", dia)
			h.DeleteException(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, quero %d — corpo: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.agenda.diaCalls != tc.wantContou {
				t.Errorf("ContarReservasNoDia chamada %d vez(es), quero %d", tc.agenda.diaCalls, tc.wantContou)
			}
			if repo.deletes != tc.wantDelete {
				t.Errorf("DeleteExcecao chamado %d vez(es), quero %d — o 409/500 tem que barrar ANTES de remover",
					repo.deletes, tc.wantDelete)
			}
		})
	}
}
