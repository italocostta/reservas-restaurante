package notification

import (
	"context"
	"log/slog"
	"net/http"

	"reservas-restaurante/internal/httpx"
)

// statusReader é a interface que o handler consome — declarada aqui, pelo
// consumidor, e satisfeita pelo *PostgresRepo sem que ele declare nada. Só o método
// que o HTTP usa, como em todos os outros pacotes deste projeto.
type statusReader interface {
	ListByStatus(ctx context.Context, status string) ([]NotificationView, error)
}

type Handler struct {
	repo statusReader
}

func NewHandler(repo statusReader) *Handler {
	return &Handler{repo: repo}
}

// statusesValidos são os quatro estados da fila. Um filtro fora disso vira 400, e não
// uma lista vazia: "?status=faild" é um erro de quem chama, não uma pergunta legítima
// que por acaso não tem resposta. Mesma decisão do enum do ?status= das reservas.
var statusesValidos = map[string]bool{
	"pending": true,
	"sending": true,
	"sent":    true,
	"failed":  true,
}

// List godoc
//
//	@Summary		Lista notificações por status (observação da fila)
//	@Description	Existe para tornar visível a notificação que esgotou as tentativas e virou `failed` — antes, ela ficava assim e ninguém era avisado. Sem `status`, devolve as `failed` (o caso de uso). Ordenadas da mais recente para a mais antiga.
//	@Tags			notificações
//	@Produce		json
//	@Param			status	query		string	false	"Filtra por status (pending, sending, sent, failed). Omitido = failed"
//	@Success		200		{array}		NotificationView
//	@Failure		400		{object}	httpx.ErrorResponse	"status inválido"
//	@Router			/notifications [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		// Default 'failed' porque é a razão de este endpoint existir (débito #11):
		// dar um lugar onde a falha silenciosa aparece.
		status = "failed"
	}
	if !statusesValidos[status] {
		httpx.Error(w, http.StatusBadRequest, "status inválido (pending, sending, sent ou failed).")
		return
	}

	lista, err := h.repo.ListByStatus(r.Context(), status)
	if err != nil {
		slog.Error("listando notificações", "erro", err, "status", status)
		httpx.Error(w, http.StatusInternalServerError, "Erro interno.")
		return
	}

	httpx.JSON(w, http.StatusOK, lista)
}
