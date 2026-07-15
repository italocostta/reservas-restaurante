package settings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"reservas-restaurante/internal/openapitest"
	"reservas-restaurante/internal/reservation"
)

const caminhoSwagger = "../../docs/swagger.json"

// fakeRepo satisfaz a interface `repository` do handler, sem banco.
type fakeRepo struct {
	settings Settings
	excecoes []Exception
}

func (f *fakeRepo) Load(context.Context) (Settings, error)            { return f.settings, nil }
func (f *fakeRepo) Save(context.Context, Settings) error              { return nil }
func (f *fakeRepo) ListExcecoes(context.Context) ([]Exception, error) { return f.excecoes, nil }
func (f *fakeRepo) SaveExcecao(context.Context, Exception) error      { return nil }
func (f *fakeRepo) DeleteExcecao(context.Context, string) error       { return nil }

func repoPadrao(t *testing.T) *fakeRepo {
	t.Helper()
	tz, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	return &fakeRepo{
		settings: Settings{
			Hours:        reservation.ServiceHours{Start: 18 * time.Hour, End: 23 * time.Hour, TZ: tz},
			OpenWeekdays: map[time.Weekday]bool{time.Monday: true},
		},
		excecoes: []Exception{{Day: "2026-12-25", IsOpen: false, Note: "Natal"}},
	}
}

func TestSettingsBateComOSwagger(t *testing.T) {
	spec := openapitest.LoadSpec(t, caminhoSwagger)

	casos := []struct {
		nome   string
		metodo string
		rota   string
		req    *http.Request
		chama  func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{
			"GET /service-hours 200", http.MethodGet, "/service-hours",
			httptest.NewRequest(http.MethodGet, "/service-hours", nil),
			(*Handler).Get,
		},
		{
			"PUT /service-hours 200", http.MethodPut, "/service-hours",
			httptest.NewRequest(http.MethodPut, "/service-hours",
				strings.NewReader(`{"start":"18:00","end":"23:00","tz":"America/Sao_Paulo","open_weekdays":[1,2,3]}`)),
			(*Handler).Update,
		},
		{
			"POST /service-exceptions 200", http.MethodPost, "/service-exceptions",
			httptest.NewRequest(http.MethodPost, "/service-exceptions",
				strings.NewReader(`{"day":"2026-12-25","is_open":false,"note":"Natal"}`)),
			(*Handler).SaveException,
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.chama(NewHandler(repoPadrao(t)), rec, tc.req)
			openapitest.RequireInContract(t, spec, tc.metodo, tc.rota, rec)
		})
	}
}
