package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"reservas-restaurante/internal/config"
	"reservas-restaurante/internal/openapitest"
)

const caminhoSwagger = "../../docs/swagger.json"

// O primeiro teste de contrato fora dos pacotes de domínio. O httpserver nunca
// precisou de um porque não tinha resposta própria — /health não está no swagger.
// O /service-hours está, e é o ÚNICO endpoint cuja razão de existir é o frontend
// não guardar uma cópia do expediente. Se o contrato dele quebrar em silêncio, a
// cópia volta a existir de fato, só que sem ninguém saber.
func TestServiceHoursBateComOSwagger(t *testing.T) {
	spec := openapitest.LoadSpec(t, caminhoSwagger)

	rec := httptest.NewRecorder()
	serviceHours(cfgDeTeste(t))(rec, httptest.NewRequest(http.MethodGet, "/service-hours", nil))

	openapitest.RequireInContract(t, spec, http.MethodGet, "/service-hours", rec)
}

// O ida-e-volta: parseHHMM("18:30") → time.Duration → hhmm() tem que devolver
// "18:30" de novo. É a única lógica de verdade do endpoint, e ela erra fácil —
// int(d.Hours()) numa duração de 18h30 dá 18 (trunca), e o minuto tem que sair
// do resto, não de d.Minutes() inteiro.
func TestServiceHoursDevolveOExpedienteConfigurado(t *testing.T) {
	cfg := cfgDeTeste(t)
	cfg.ServiceStart = 18*time.Hour + 30*time.Minute
	cfg.ServiceEnd = 23 * time.Hour

	rec := httptest.NewRecorder()
	serviceHours(cfg)(rec, httptest.NewRequest(http.MethodGet, "/service-hours", nil))

	var got ServiceHours
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("corpo não é JSON: %v (corpo: %s)", err, rec.Body)
	}

	quero := ServiceHours{Start: "18:30", End: "23:00", TZ: "America/Sao_Paulo"}
	if got != quero {
		t.Errorf("expediente = %+v, quero %+v", got, quero)
	}
}

func cfgDeTeste(t *testing.T) config.Config {
	t.Helper()

	tz, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatalf("carregando fuso: %v", err)
	}

	return config.Config{
		ServiceStart: 18 * time.Hour,
		ServiceEnd:   23 * time.Hour,
		ServiceTZ:    tz,
	}
}
