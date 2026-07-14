package table

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
)

// carregaSpec lê o swagger.json que o `swag init` gerou. O swaggo v1 emite
// Swagger 2.0; o validador só fala OpenAPI 3 — daí a conversão.
func carregaSpec(t *testing.T) *openapi3.T {
	t.Helper()

	bruto, err := os.ReadFile("../../docs/swagger.json")
	if err != nil {
		t.Fatalf("swagger.json ausente — rode `swag init -g cmd/api/main.go -o docs --parseInternal`: %v", err)
	}

	var v2 openapi2.T
	if err := json.Unmarshal(bruto, &v2); err != nil {
		t.Fatalf("swagger.json inválido: %v", err)
	}

	v3, err := openapi2conv.ToV3(&v2)
	if err != nil {
		t.Fatalf("convertendo para OpenAPI 3: %v", err)
	}

	// Reserializa e recarrega pelo loader: é o que resolve os $ref para os
	// schemas de components. Sem isso, Schema.Value vem nil.
	raw, err := json.Marshal(v3)
	if err != nil {
		t.Fatalf("reserializando spec: %v", err)
	}
	spec, err := openapi3.NewLoader().LoadFromData(raw)
	if err != nil {
		t.Fatalf("recarregando spec: %v", err)
	}

	return spec
}

// exigeRespostaNoContrato falha se o status devolvido não estiver DECLARADO na
// anotação do handler, ou se o corpo não bater com o schema declarado.
// É esta função que impede a documentação de mentir.
func exigeRespostaNoContrato(t *testing.T, spec *openapi3.T, metodo, rota string, rec *httptest.ResponseRecorder) {
	t.Helper()

	item := spec.Paths.Find(rota)
	if item == nil {
		t.Fatalf("rota %q não existe no swagger.json", rota)
	}
	op := item.GetOperation(metodo)
	if op == nil {
		t.Fatalf("%s %s não existe no swagger.json", metodo, rota)
	}

	resp := op.Responses.Status(rec.Code)
	if resp == nil || resp.Value == nil {
		t.Fatalf("%s %s devolveu %d, que NÃO está declarado no swagger — a anotação está mentindo",
			metodo, rota, rec.Code)
	}

	media := resp.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		return // resposta declarada sem corpo
	}

	var corpo any
	if err := json.Unmarshal(rec.Body.Bytes(), &corpo); err != nil {
		t.Fatalf("%s %s (%d): corpo não é JSON válido: %v", metodo, rota, rec.Code, err)
	}

	if err := media.Schema.Value.VisitJSON(corpo); err != nil {
		t.Errorf("%s %s (%d): corpo não bate com o schema declarado: %v\ncorpo: %s",
			metodo, rota, rec.Code, err, rec.Body)
	}
}

func TestRespostasBatemComOSwagger(t *testing.T) {
	spec := carregaSpec(t)

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

			tc.chama(NewHandler(tc.repo), rec, tc.req)

			exigeRespostaNoContrato(t, spec, tc.metodo, tc.rota, rec)
		})
	}
}

func comPathValue(r *http.Request, id uuid.UUID) *http.Request {
	r.SetPathValue("id", id.String())
	return r
}
