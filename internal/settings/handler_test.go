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
	saves int
}

func (s *repoStub) Load(context.Context) (Settings, error) {
	tz, _ := time.LoadLocation("America/Sao_Paulo")
	return Settings{
		Hours:        reservation.ServiceHours{Start: 18 * time.Hour, End: 23 * time.Hour, TZ: tz},
		OpenWeekdays: map[time.Weekday]bool{time.Monday: true},
	}, nil
}
func (s *repoStub) Save(context.Context, Settings) error              { s.saves++; return nil }
func (s *repoStub) ListExcecoes(context.Context) ([]Exception, error) { return nil, nil }
func (s *repoStub) SaveExcecao(context.Context, Exception) error      { return nil }
func (s *repoStub) DeleteExcecao(context.Context, string) error       { return nil }

// agendaStub é o dublê configurável da contagem: n reservas fora, ou um erro ao
// contar. Registra as chamadas para o teste provar que a contagem foi consultada.
type agendaStub struct {
	n       int
	proxima string
	err     error
	calls   int
}

func (a *agendaStub) ContarReservasForaDoExpediente(context.Context, reservation.ServiceHours, []int) (int, string, error) {
	a.calls++
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
