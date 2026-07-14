package httpserver

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"reservas-restaurante/internal/httpx"
)

// Middleware embrulha um handler e devolve outro. Não existe @Aspect, proxy
// dinâmico nem ordem por anotação: é composição de funções, e a ordem de
// execução é literalmente a ordem em que você as escreve na lista.
type Middleware func(http.Handler) http.Handler

// Chain aplica as middlewares de fora para dentro: a PRIMEIRA da lista é a mais
// externa — a primeira a ver a requisição e a última a ver a resposta.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// statusRecorder existe porque http.ResponseWriter só deixa ESCREVER o status,
// nunca lê-lo de volta. Para logar o status da resposta é preciso interceptar
// a chamada de WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// 200 como padrão: um handler que escreve o corpo sem chamar
		// WriteHeader devolve 200 implicitamente.
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		slog.Info("requisição",
			"metodo", r.Method,
			"rota", r.URL.Path,
			"status", rec.status,
			"duracao", time.Since(start),
		)
	})
}

// Recover transforma panic em 500 com stack no log. O net/http já tem um
// recover próprio que impede o processo de morrer, mas ele fecha a conexão sem
// resposta nenhuma — o cliente vê a conexão cair, não um erro.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				slog.Error("panic no handler",
					"panic", p,
					"rota", r.URL.Path,
					"stack", string(debug.Stack()),
				)
				httpx.Error(w, http.StatusInternalServerError, "erro interno.")
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// CORS libera só a origem configurada — nunca "*". O "*" é o default que alguém
// põe "só para testar" e que sobrevive até produção.
func CORS(allowedOrigin string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Origin") == allowedOrigin {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				// Sem Vary, um proxy pode cachear a resposta de uma origem e
				// entregá-la para outra.
				w.Header().Set("Vary", "Origin")
			}

			// Preflight morre aqui: não chega no handler.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// MaxBytes corta o corpo da requisição no limite. Sem isto, um POST de 2 GB é
// lido inteiro para a memória — a lacuna que ficou anotada no table/handler.go.
func MaxBytes(limit int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}
