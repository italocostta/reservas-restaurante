// Package openapitest valida respostas HTTP reais contra o swagger.json que o
// `swag init` gerou. É o que impede a documentação code-first de mentir: sem
// isto, nada garante que a anotação @Success 201 do handler seja verdade.
//
// Só testes usam este pacote — ele existe como pacote normal (e não como
// arquivo _test.go) porque arquivos de teste não são importáveis entre pacotes,
// e tanto `table` quanto `reservation` precisam do mesmo validador.
package openapitest

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
)

// LoadSpec lê o swagger.json e o converte para OpenAPI 3 — o swaggo v1 emite
// Swagger 2.0, e o validador só fala v3.
func LoadSpec(t *testing.T, caminho string) *openapi3.T {
	t.Helper()

	bruto, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatalf("swagger.json ausente em %s — rode `swag init -g cmd/api/main.go -o docs --parseInternal`: %v", caminho, err)
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
	// schemas de components. Sem isso, Schema.Value vem nil e a validação
	// passaria por vazio — falso verde.
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

// RequireInContract falha se o status devolvido não estiver DECLARADO na
// anotação do handler, ou se o corpo não bater com o schema declarado.
//
// `rota` é o template como aparece no swagger ("/tables/{id}"), não a URL real.
func RequireInContract(t *testing.T, spec *openapi3.T, metodo, rota string, rec *httptest.ResponseRecorder) {
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
		return // resposta declarada sem corpo (ex: 204)
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
