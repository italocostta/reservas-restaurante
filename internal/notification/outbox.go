package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Kind string

const (
	KindConfirmed Kind = "reservation_confirmed"
	KindCancelled Kind = "reservation_cancelled"
	// KindUpdated é a edição: a reserva foi remarcada (novo horário, mesa ou
	// tamanho). Um evento SÓ, no lugar do par cancelamento+confirmação que a
	// edição produz por baixo — o cliente recebe "sua reserva foi alterada", não
	// "foi cancelada" seguido de "foi confirmada", que o assustaria à toa.
	KindUpdated Kind = "reservation_updated"
)

// Payload é o snapshot do evento, congelado no instante em que ele aconteceu.
// Não é uma referência à reserva — é uma CÓPIA do que ela era.
//
// Se o worker fizesse JOIN com `reservations` na hora de enviar, uma notificação
// de "confirmada" despachada com atraso poderia sair descrevendo uma reserva já
// cancelada. O outbox carrega o fato como ele FOI, não como ele ESTÁ.
type Payload struct {
	CustomerName  string    `json:"customer_name"`
	CustomerPhone string    `json:"customer_phone"`
	PartySize     int       `json:"party_size"`
	StartsAt      time.Time `json:"starts_at"`
	EndsAt        time.Time `json:"ends_at"`
}

const enqueueSQL = `
INSERT INTO notifications (reservation_id, kind, payload)
VALUES ($1, $2, $3)`

// Enqueue recebe uma `pgx.Tx` — a transação DE QUEM CHAMA — e não um pool.
//
// É essa assinatura que dá o outbox: quem grava a reserva grava o evento na
// mesma transação, e o banco garante que ou existem os dois, ou nenhum. Se
// Enqueue abrisse a própria conexão, voltaríamos ao problema que o outbox
// existe para resolver: o processo morre entre um COMMIT e o outro, e a
// notificação some (ou pior, sobra uma notificação de reserva que não existe).
//
// O preço é honesto e vale registrar: este é o único ponto do pacote
// `notification` que aparece na assinatura pública com um tipo do `pgx`.
// Atomicidade é um conceito de banco de dados — não dá para abstraí-la sem
// mentir sobre o que ela custa.
func Enqueue(ctx context.Context, tx pgx.Tx, reservationID uuid.UUID, kind Kind, p Payload) error {
	bruto, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("serializando payload da notificação: %w", err)
	}

	if _, err := tx.Exec(ctx, enqueueSQL, reservationID, kind, bruto); err != nil {
		return fmt.Errorf("enfileirando notificação: %w", err)
	}

	return nil
}
