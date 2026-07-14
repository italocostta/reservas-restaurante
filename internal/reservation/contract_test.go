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

// Fakes das TRÊS interfaces que o handler consome. Nenhuma delas é a mesma que
// o allocator consome — cada consumidor declara a sua, e o *PostgresRepo real
// satisfaz todas sem saber que existem.

type fakeAllocator struct {
	fn func(ctx context.Context, req AllocationRequest) (Reservation, error)
}

func (f fakeAllocator) CreateReservation(ctx context.Context, req AllocationRequest) (Reservation, error) {
	return f.fn(ctx, req)
}

type fakeRepo struct {
	getFn    func(context.Context, uuid.UUID) (Reservation, error)
	listFn   func(context.Context, ListFilter) ([]Reservation, error)
	cancelFn func(context.Context, uuid.UUID) error
}

func (f fakeRepo) Get(ctx context.Context, id uuid.UUID) (Reservation, error) {
	return f.getFn(ctx, id)
}

func (f fakeRepo) List(ctx context.Context, filtro ListFilter) ([]Reservation, error) {
	return f.listFn(ctx, filtro)
}

func (f fakeRepo) Cancel(ctx context.Context, id uuid.UUID) error {
	return f.cancelFn(ctx, id)
}

type fakeSchedule struct {
	fn func(context.Context, uuid.UUID, string) ([]Window, error)
}

func (f fakeSchedule) FreeWindows(ctx context.Context, id uuid.UUID, dia string) ([]Window, error) {
	return f.fn(ctx, id, dia)
}

func comPathValue(r *http.Request, id uuid.UUID) *http.Request {
	r.SetPathValue("id", id.String())
	return r
}

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

	corpoValido := `{"customer_name":"Maria Silva","customer_phone":"11999998888","party_size":4,` +
		`"starts_at":"2026-07-20T19:00:00-03:00","ends_at":"2026-07-20T21:00:00-03:00"}`

	casos := []struct {
		nome      string
		metodo    string
		rota      string
		allocator fakeAllocator
		repo      fakeRepo
		schedule  fakeSchedule
		req       *http.Request
		chama     func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{
			nome: "POST /reservations 201", metodo: http.MethodPost, rota: "/reservations",
			allocator: fakeAllocator{fn: func(context.Context, AllocationRequest) (Reservation, error) {
				return res, nil
			}},
			req:   httptest.NewRequest(http.MethodPost, "/reservations", strings.NewReader(corpoValido)),
			chama: (*Handler).Create,
		},
		{
			nome: "POST /reservations 400 (validação)", metodo: http.MethodPost, rota: "/reservations",
			allocator: fakeAllocator{fn: func(context.Context, AllocationRequest) (Reservation, error) {
				return Reservation{}, invalido("Grupo de 6 pessoas excede a capacidade da mesa (4).")
			}},
			req:   httptest.NewRequest(http.MethodPost, "/reservations", strings.NewReader(corpoValido)),
			chama: (*Handler).Create,
		},
		{
			nome: "POST /reservations 409 (sem disponibilidade)", metodo: http.MethodPost, rota: "/reservations",
			allocator: fakeAllocator{fn: func(context.Context, AllocationRequest) (Reservation, error) {
				return Reservation{}, ErrNoAvailability
			}},
			req:   httptest.NewRequest(http.MethodPost, "/reservations", strings.NewReader(corpoValido)),
			chama: (*Handler).Create,
		},
		{
			nome: "POST /reservations 409 (mesa pedida ocupada)", metodo: http.MethodPost, rota: "/reservations",
			allocator: fakeAllocator{fn: func(context.Context, AllocationRequest) (Reservation, error) {
				return Reservation{}, ErrTableUnavailable
			}},
			req:   httptest.NewRequest(http.MethodPost, "/reservations", strings.NewReader(corpoValido)),
			chama: (*Handler).Create,
		},
		{
			nome: "GET /reservations 200", metodo: http.MethodGet, rota: "/reservations",
			repo: fakeRepo{listFn: func(context.Context, ListFilter) ([]Reservation, error) {
				return []Reservation{res}, nil
			}},
			req:   httptest.NewRequest(http.MethodGet, "/reservations?date=2026-07-20&status=confirmed", nil),
			chama: (*Handler).List,
		},
		{
			nome: "GET /reservations 400 (status inválido)", metodo: http.MethodGet, rota: "/reservations",
			req:   httptest.NewRequest(http.MethodGet, "/reservations?status=talvez", nil),
			chama: (*Handler).List,
		},
		{
			nome: "GET /reservations/{id} 200", metodo: http.MethodGet, rota: "/reservations/{id}",
			repo: fakeRepo{getFn: func(context.Context, uuid.UUID) (Reservation, error) {
				return res, nil
			}},
			req:   comPathValue(httptest.NewRequest(http.MethodGet, "/reservations/"+id.String(), nil), id),
			chama: (*Handler).Get,
		},
		{
			nome: "GET /reservations/{id} 404", metodo: http.MethodGet, rota: "/reservations/{id}",
			repo: fakeRepo{getFn: func(context.Context, uuid.UUID) (Reservation, error) {
				return Reservation{}, ErrNotFound
			}},
			req:   comPathValue(httptest.NewRequest(http.MethodGet, "/reservations/"+id.String(), nil), id),
			chama: (*Handler).Get,
		},
		{
			nome: "DELETE /reservations/{id} 204", metodo: http.MethodDelete, rota: "/reservations/{id}",
			repo:  fakeRepo{cancelFn: func(context.Context, uuid.UUID) error { return nil }},
			req:   comPathValue(httptest.NewRequest(http.MethodDelete, "/reservations/"+id.String(), nil), id),
			chama: (*Handler).Delete,
		},
		{
			nome: "DELETE /reservations/{id} 404", metodo: http.MethodDelete, rota: "/reservations/{id}",
			repo:  fakeRepo{cancelFn: func(context.Context, uuid.UUID) error { return ErrNotFound }},
			req:   comPathValue(httptest.NewRequest(http.MethodDelete, "/reservations/"+id.String(), nil), id),
			chama: (*Handler).Delete,
		},
		{
			nome: "GET /tables/{id}/availability 200", metodo: http.MethodGet, rota: "/tables/{id}/availability",
			schedule: fakeSchedule{fn: func(context.Context, uuid.UUID, string) ([]Window, error) {
				return []Window{jan(20, 18, 0, 20, 19, 0), jan(20, 21, 0, 20, 23, 0)}, nil
			}},
			req:   comPathValue(httptest.NewRequest(http.MethodGet, "/tables/"+mesaID.String()+"/availability?date=2026-07-20", nil), mesaID),
			chama: (*Handler).Availability,
		},
		{
			nome: "GET /tables/{id}/availability 400 (sem date)", metodo: http.MethodGet, rota: "/tables/{id}/availability",
			req:   comPathValue(httptest.NewRequest(http.MethodGet, "/tables/"+mesaID.String()+"/availability", nil), mesaID),
			chama: (*Handler).Availability,
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			rec := httptest.NewRecorder()

			tc.chama(NewHandler(tc.allocator, tc.repo, tc.schedule), rec, tc.req)

			openapitest.RequireInContract(t, spec, tc.metodo, tc.rota, rec)
		})
	}
}
