package reservation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"reservas-restaurante/internal/openapitest"
)

const caminhoSwagger = "../../docs/swagger.json"

// Os fakes e o `corpoValido` vivem no handler_test.go — mesmo pacote, mesmos
// consumidores. Aqui só se valida o CONTRATO: o status devolvido está declarado
// na anotação do handler? O corpo bate com o schema? É o que impede a doc
// code-first de mentir.
func TestRespostasBatemComOSwagger(t *testing.T) {
	spec := openapitest.LoadSpec(t, caminhoSwagger)

	id := uuid.New()
	mesaID := uuid.New()
	res := Reservation{
		ID: id, TableID: mesaID,
		CustomerName: "Maria Silva", CustomerPhone: "11999998888",
		PartySize: 4,
		StartsAt:  em(20, 19, 0), EndsAt: em(20, 21, 0),
		Status:    StatusConfirmed,
		CreatedAt: time.Now(),
	}

	casos := []struct {
		nome      string
		metodo    string
		rota      string // o template, como aparece no swagger
		allocator *fakeAllocator
		repo      *fakeRepo
		schedule  *fakeSchedule
		req       *http.Request
		chama     func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{
			nome: "POST /reservations 201", metodo: http.MethodPost, rota: "/reservations",
			allocator: &fakeAllocator{fn: func(context.Context, AllocationRequest) (Reservation, error) {
				return res, nil
			}},
			req:   httptest.NewRequest(http.MethodPost, "/reservations", strings.NewReader(corpoValido)),
			chama: (*Handler).Create,
		},
		{
			nome: "POST /reservations 400", metodo: http.MethodPost, rota: "/reservations",
			allocator: &fakeAllocator{fn: func(context.Context, AllocationRequest) (Reservation, error) {
				return Reservation{}, invalido("Grupo de 6 pessoas excede a capacidade da mesa (4).")
			}},
			req:   httptest.NewRequest(http.MethodPost, "/reservations", strings.NewReader(corpoValido)),
			chama: (*Handler).Create,
		},
		{
			nome: "POST /reservations 409 (sem disponibilidade)", metodo: http.MethodPost, rota: "/reservations",
			allocator: &fakeAllocator{fn: func(context.Context, AllocationRequest) (Reservation, error) {
				return Reservation{}, ErrNoAvailability
			}},
			req:   httptest.NewRequest(http.MethodPost, "/reservations", strings.NewReader(corpoValido)),
			chama: (*Handler).Create,
		},
		{
			nome: "POST /reservations 409 (mesa pedida ocupada)", metodo: http.MethodPost, rota: "/reservations",
			allocator: &fakeAllocator{fn: func(context.Context, AllocationRequest) (Reservation, error) {
				return Reservation{}, ErrTableUnavailable
			}},
			req:   httptest.NewRequest(http.MethodPost, "/reservations", strings.NewReader(corpoValido)),
			chama: (*Handler).Create,
		},
		{
			nome: "GET /reservations 200", metodo: http.MethodGet, rota: "/reservations",
			repo: &fakeRepo{listFn: func(context.Context, ListFilter) ([]Reservation, error) {
				return []Reservation{res}, nil
			}},
			req:   httptest.NewRequest(http.MethodGet, "/reservations?date=2026-07-20&status=confirmed", nil),
			chama: (*Handler).List,
		},
		{
			nome: "GET /reservations 400", metodo: http.MethodGet, rota: "/reservations",
			req:   httptest.NewRequest(http.MethodGet, "/reservations?status=talvez", nil),
			chama: (*Handler).List,
		},
		{
			nome: "GET /reservations/{id} 200", metodo: http.MethodGet, rota: "/reservations/{id}",
			repo: &fakeRepo{getFn: func(context.Context, uuid.UUID) (Reservation, error) {
				return res, nil
			}},
			req:   comPathValue(httptest.NewRequest(http.MethodGet, "/reservations/"+id.String(), nil), id),
			chama: (*Handler).Get,
		},
		{
			nome: "GET /reservations/{id} 404", metodo: http.MethodGet, rota: "/reservations/{id}",
			repo: &fakeRepo{getFn: func(context.Context, uuid.UUID) (Reservation, error) {
				return Reservation{}, ErrNotFound
			}},
			req:   comPathValue(httptest.NewRequest(http.MethodGet, "/reservations/"+id.String(), nil), id),
			chama: (*Handler).Get,
		},
		{
			nome: "DELETE /reservations/{id} 204", metodo: http.MethodDelete, rota: "/reservations/{id}",
			repo:  &fakeRepo{cancelFn: func(context.Context, uuid.UUID) error { return nil }},
			req:   comPathValue(httptest.NewRequest(http.MethodDelete, "/reservations/"+id.String(), nil), id),
			chama: (*Handler).Delete,
		},
		{
			nome: "DELETE /reservations/{id} 404", metodo: http.MethodDelete, rota: "/reservations/{id}",
			repo:  &fakeRepo{cancelFn: func(context.Context, uuid.UUID) error { return ErrNotFound }},
			req:   comPathValue(httptest.NewRequest(http.MethodDelete, "/reservations/"+id.String(), nil), id),
			chama: (*Handler).Delete,
		},
		{
			nome: "GET /tables/{id}/availability 200", metodo: http.MethodGet, rota: "/tables/{id}/availability",
			schedule: &fakeSchedule{fn: func(context.Context, uuid.UUID, string) ([]Window, error) {
				return []Window{jan(20, 18, 0, 20, 19, 0), jan(20, 21, 0, 20, 23, 0)}, nil
			}},
			req:   comPathValue(httptest.NewRequest(http.MethodGet, "/tables/"+mesaID.String()+"/availability?date=2026-07-20", nil), mesaID),
			chama: (*Handler).Availability,
		},
		{
			nome: "GET /tables/{id}/availability 400", metodo: http.MethodGet, rota: "/tables/{id}/availability",
			req:   comPathValue(httptest.NewRequest(http.MethodGet, "/tables/"+mesaID.String()+"/availability", nil), mesaID),
			chama: (*Handler).Availability,
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			// Fakes nil viram vazios: o caso que não usa aquela dependência
			// simplesmente nunca a chama.
			alloc, repo, sched := tc.allocator, tc.repo, tc.schedule
			if alloc == nil {
				alloc = &fakeAllocator{}
			}
			if repo == nil {
				repo = &fakeRepo{}
			}
			if sched == nil {
				sched = &fakeSchedule{}
			}

			rec := httptest.NewRecorder()

			tc.chama(NewHandler(alloc, repo, sched), rec, tc.req)

			openapitest.RequireInContract(t, spec, tc.metodo, tc.rota, rec)
		})
	}
}
