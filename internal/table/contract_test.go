package table

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

func TestRespostasBatemComOSwagger(t *testing.T) {
	spec := openapitest.LoadSpec(t, caminhoSwagger)

	id := uuid.New()
	mesa := Table{ID: id, Name: "Mesa 12", Capacity: 4, IsActive: true, CreatedAt: time.Now()}

	// `chama` é uma method expression: (*Handler).Create tem tipo
	// func(*Handler, http.ResponseWriter, *http.Request). O método vira valor,
	// com o receiver virando o primeiro parâmetro.
	casos := []struct {
		nome   string
		metodo string
		rota   string // o template, como aparece no swagger
		repo   *fakeRepo
		req    *http.Request
		chama  func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{
			nome: "POST /tables 201", metodo: http.MethodPost, rota: "/tables",
			repo:  &fakeRepo{createFn: func(context.Context, string, int) (Table, error) { return mesa, nil }},
			req:   httptest.NewRequest(http.MethodPost, "/tables", strings.NewReader(`{"name":"Mesa 12","capacity":4}`)),
			chama: (*Handler).Create,
		},
		{
			nome: "POST /tables 409", metodo: http.MethodPost, rota: "/tables",
			repo:  &fakeRepo{createFn: func(context.Context, string, int) (Table, error) { return Table{}, ErrDuplicateName }},
			req:   httptest.NewRequest(http.MethodPost, "/tables", strings.NewReader(`{"name":"Mesa 12","capacity":4}`)),
			chama: (*Handler).Create,
		},
		{
			nome: "POST /tables 400", metodo: http.MethodPost, rota: "/tables",
			repo:  &fakeRepo{},
			req:   httptest.NewRequest(http.MethodPost, "/tables", strings.NewReader(`{"name":"Mesa X","capacity":0}`)),
			chama: (*Handler).Create,
		},
		{
			nome: "GET /tables 200", metodo: http.MethodGet, rota: "/tables",
			repo:  &fakeRepo{listFn: func(context.Context, *bool) ([]Table, error) { return []Table{mesa}, nil }},
			req:   httptest.NewRequest(http.MethodGet, "/tables", nil),
			chama: (*Handler).List,
		},
		{
			nome: "GET /tables 400", metodo: http.MethodGet, rota: "/tables",
			repo:  &fakeRepo{},
			req:   httptest.NewRequest(http.MethodGet, "/tables?active=talvez", nil),
			chama: (*Handler).List,
		},
		{
			nome: "GET /tables/{id} 200", metodo: http.MethodGet, rota: "/tables/{id}",
			repo:  &fakeRepo{getFn: func(context.Context, uuid.UUID) (Table, error) { return mesa, nil }},
			req:   comPathValue(httptest.NewRequest(http.MethodGet, "/tables/"+id.String(), nil), id),
			chama: (*Handler).Get,
		},
		{
			nome: "GET /tables/{id} 404", metodo: http.MethodGet, rota: "/tables/{id}",
			repo:  &fakeRepo{getFn: func(context.Context, uuid.UUID) (Table, error) { return Table{}, ErrNotFound }},
			req:   comPathValue(httptest.NewRequest(http.MethodGet, "/tables/"+id.String(), nil), id),
			chama: (*Handler).Get,
		},
		{
			nome: "PATCH /tables/{id} 200", metodo: http.MethodPatch, rota: "/tables/{id}",
			repo:  &fakeRepo{updateFn: func(context.Context, uuid.UUID, UpdateParams) (Table, error) { return mesa, nil }},
			req:   comPathValue(httptest.NewRequest(http.MethodPatch, "/tables/"+id.String(), strings.NewReader(`{"is_active":false}`)), id),
			chama: (*Handler).Update,
		},
		{
			nome: "PATCH /tables/{id} 404", metodo: http.MethodPatch, rota: "/tables/{id}",
			repo:  &fakeRepo{updateFn: func(context.Context, uuid.UUID, UpdateParams) (Table, error) { return Table{}, ErrNotFound }},
			req:   comPathValue(httptest.NewRequest(http.MethodPatch, "/tables/"+id.String(), strings.NewReader(`{"capacity":6}`)), id),
			chama: (*Handler).Update,
		},
		{
			nome: "PATCH /tables/{id} 409", metodo: http.MethodPatch, rota: "/tables/{id}",
			repo:  &fakeRepo{updateFn: func(context.Context, uuid.UUID, UpdateParams) (Table, error) { return Table{}, ErrDuplicateName }},
			req:   comPathValue(httptest.NewRequest(http.MethodPatch, "/tables/"+id.String(), strings.NewReader(`{"name":"Mesa 12"}`)), id),
			chama: (*Handler).Update,
		},
		{
			nome: "PATCH /tables/{id} 400", metodo: http.MethodPatch, rota: "/tables/{id}",
			repo:  &fakeRepo{},
			req:   comPathValue(httptest.NewRequest(http.MethodPatch, "/tables/"+id.String(), strings.NewReader(`{}`)), id),
			chama: (*Handler).Update,
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			rec := httptest.NewRecorder()

			tc.chama(NewHandler(tc.repo, agendaVazia()), rec, tc.req)

			openapitest.RequireInContract(t, spec, tc.metodo, tc.rota, rec)
		})
	}
}

func comPathValue(r *http.Request, id uuid.UUID) *http.Request {
	r.SetPathValue("id", id.String())
	return r
}
