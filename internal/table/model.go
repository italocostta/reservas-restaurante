package table

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Erros de domínio do pacote. repository.go é o único lugar que conhece
// códigos do Postgres e os traduz para cá — nenhum pgconn.PgError atravessa
// a fronteira do repositório.
var (
	ErrNotFound      = errors.New("mesa não encontrada")
	ErrDuplicateName = errors.New("já existe uma mesa com esse nome")
)

// Table espelha uma linha de restaurant_tables.
type Table struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Capacity  int       `json:"capacity"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}
