package httpserver

import (
	"net/http"

	httpSwagger "github.com/swaggo/http-swagger/v2"

	// Import em branco: o init() do pacote gerado registra o swagger.json no
	// swaggo. Nada aqui referencia um identificador de `docs` diretamente.
	_ "reservas-restaurante/docs"

	"reservas-restaurante/internal/config"
	"reservas-restaurante/internal/httpx"
	"reservas-restaurante/internal/reservation"
	"reservas-restaurante/internal/table"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// New monta as rotas e devolve o handler raiz já embrulhado nas middlewares.
func New(cfg config.Config, tables *table.Handler, reservations *reservation.Handler) http.Handler {
	mux := http.NewServeMux()

	// Método e caminho na mesma string — recurso do ServeMux desde o Go 1.22.
	// "POST /tables" casa só POST; um GET no mesmo caminho recebe 405 sem que
	// eu escreva uma linha para isso.
	mux.HandleFunc("POST /tables", tables.Create)
	mux.HandleFunc("GET /tables", tables.List)
	mux.HandleFunc("GET /tables/{id}", tables.Get)
	mux.HandleFunc("PATCH /tables/{id}", tables.Update)

	// Rota de mesa, handler de reserva. A URL não é a fronteira do domínio:
	// "quais janelas desta mesa estão livres?" só se responde olhando as
	// reservas. O ServeMux resolve a especificidade sozinho — /tables/{id}/
	// availability não conflita com /tables/{id}.
	mux.HandleFunc("GET /tables/{id}/availability", reservations.Availability)

	mux.HandleFunc("POST /reservations", reservations.Create)
	mux.HandleFunc("GET /reservations", reservations.List)
	mux.HandleFunc("GET /reservations/{id}", reservations.Get)
	mux.HandleFunc("DELETE /reservations/{id}", reservations.Delete)

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// A barra final faz o ServeMux casar tudo abaixo de /swagger/ — a UI puxa
	// vários arquivos (index.html, doc.json, os assets).
	mux.Handle("GET /swagger/", httpSwagger.WrapHandler)

	// Ordem de fora para dentro. Logger é o mais externo de propósito: com
	// Recover logo abaixo dele, uma requisição que dá panic produz DUAS linhas
	// de log — o panic com stack (Recover) e a linha normal com status 500
	// (Logger), porque nesse ponto o Recover já converteu o panic em resposta.
	// Se o Recover fosse o mais externo, o panic escaparia do Logger e a
	// requisição sumiria do log de acesso.
	return Chain(mux,
		Logger,
		Recover,
		CORS(cfg.CORSAllowedOrigin),
		MaxBytes(maxBodyBytes),
	)
}
