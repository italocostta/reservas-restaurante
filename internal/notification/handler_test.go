package notification

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeReader registra o status que o handler pediu — é como o teste prova que o
// default (sem ?status=) consulta 'failed', a razão de existir do endpoint.
type fakeReader struct {
	pedido string
	lista  []NotificationView
	err    error
}

func (f *fakeReader) ListByStatus(_ context.Context, status string) ([]NotificationView, error) {
	f.pedido = status
	return f.lista, f.err
}

func TestNotificationsList(t *testing.T) {
	casos := []struct {
		nome       string
		query      string
		err        error
		wantStatus int
		wantPedido string // o status que o repo deveria ter recebido ("" = repo não chamado)
	}{
		{
			nome:       "sem status → default failed",
			query:      "",
			wantStatus: http.StatusOK, wantPedido: "failed",
		},
		{
			nome:       "status explícito é respeitado",
			query:      "?status=sent",
			wantStatus: http.StatusOK, wantPedido: "sent",
		},
		{
			// Filtro fora do enum é erro de quem chama, não lista vazia — e o repo nem
			// chega a ser consultado.
			nome:       "status inválido → 400, sem consultar o repo",
			query:      "?status=faild",
			wantStatus: http.StatusBadRequest, wantPedido: "",
		},
		{
			nome:       "erro do repo → 500",
			query:      "?status=failed",
			err:        errors.New("db caiu"),
			wantStatus: http.StatusInternalServerError, wantPedido: "failed",
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			repo := &fakeReader{err: tc.err}
			h := NewHandler(repo)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/notifications"+tc.query, nil)
			h.List(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, quero %d — corpo: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if repo.pedido != tc.wantPedido {
				t.Errorf("repo consultado com status %q, quero %q", repo.pedido, tc.wantPedido)
			}
		})
	}
}
