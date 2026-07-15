package httpserver

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"reservas-restaurante/internal/config"
	"reservas-restaurante/internal/notification"
	"reservas-restaurante/internal/reservation"
	"reservas-restaurante/internal/settings"
	"reservas-restaurante/internal/table"
)

const origemTeste = "http://localhost:5173"

// rotas espelha o router.go de propósito: é a lista de métodos que o preflight de
// CORS precisa cobrir. Um método novo aqui que o Access-Control-Allow-Methods não
// anuncie faz o teste falhar — exatamente o bug do PUT /service-hours, que o
// router atendia mas o CORS não listava, e o browser recusava no preflight.
var rotas = []struct {
	metodo string
	path   string
}{
	{http.MethodPost, "/tables"},
	{http.MethodGet, "/tables"},
	{http.MethodGet, "/tables/x"},
	{http.MethodPatch, "/tables/x"},
	{http.MethodGet, "/tables/x/availability"},
	{http.MethodGet, "/availability"},
	{http.MethodPost, "/reservations"},
	{http.MethodGet, "/reservations"},
	{http.MethodGet, "/reservations/x"},
	{http.MethodPatch, "/reservations/x"},
	{http.MethodDelete, "/reservations/x"},
	{http.MethodGet, "/service-hours"},
	{http.MethodPut, "/service-hours"},
	{http.MethodPost, "/service-exceptions"},
	{http.MethodDelete, "/service-exceptions/x"},
	{http.MethodGet, "/notifications"},
}

// routerDeTeste monta o router real. Os handlers vão com dependências nil de
// propósito: um preflight OPTIONS é interceptado pela middleware de CORS e devolve
// 204 ANTES de chegar no mux, então nenhum método de handler é chamado.
func routerDeTeste() http.Handler {
	tables := table.NewHandler(nil, nil)
	reservations := reservation.NewHandler(nil, nil, nil)
	cfgRest := settings.NewHandler(nil, nil)
	notifs := notification.NewHandler(nil)
	cfg := config.Config{CORSAllowedOrigin: origemTeste}
	return New(cfg, tables, reservations, cfgRest, notifs)
}

// Todo método que o router atende tem que estar no Access-Control-Allow-Methods do
// preflight — senão o browser recusa a requisição real. Foi o que travou o Salvar
// expediente: PUT /service-hours existia no router mas faltava no CORS.
func TestPreflightLiberaTodosOsMetodosDasRotas(t *testing.T) {
	router := routerDeTeste()

	for _, r := range rotas {
		t.Run(r.metodo+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, r.path, nil)
			req.Header.Set("Origin", origemTeste)
			req.Header.Set("Access-Control-Request-Method", r.metodo)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("preflight de %s %s: status %d, esperado 204", r.metodo, r.path, rec.Code)
			}

			// Match por token, não por substring: evita que "PUT" passasse só por
			// ser pedaço de outra palavra (não é o caso hoje, mas o teste não deve
			// depender disso).
			permitidos := strings.Split(rec.Header().Get("Access-Control-Allow-Methods"), ", ")
			if !slices.Contains(permitidos, r.metodo) {
				t.Fatalf("preflight de %s %s: método ausente em Access-Control-Allow-Methods (%q)",
					r.metodo, r.path, permitidos)
			}
		})
	}
}
