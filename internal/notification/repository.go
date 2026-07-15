package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Notification é uma linha da fila, já reivindicada por este worker.
type Notification struct {
	ID            uuid.UUID
	ReservationID uuid.UUID
	Kind          Kind
	Payload       Payload
	Attempts      int // já inclui a tentativa atual
}

type PostgresRepo struct {
	db *pgxpool.Pool
}

func NewPostgresRepo(db *pgxpool.Pool) *PostgresRepo {
	return &PostgresRepo{db: db}
}

// A query que faz a fila funcionar. Três coisas acontecem atomicamente num único
// statement: seleciona as candidatas, TRAVA as linhas, e as marca como 'sending'.
//
// FOR UPDATE SKIP LOCKED — e aqui está a ironia deliciosa deste projeto. A spec
// DESCARTOU o SELECT FOR UPDATE para reservas, e com toda a razão: lock pessimista
// não modela disponibilidade baseada em intervalo de tempo, e geraria contenção
// entre reservas da mesma mesa em horários que nem se sobrepõem.
//
// Aqui ele é exatamente a ferramenta certa. A diferença não é a primitiva — é o
// que se está travando. Lá, "a mesa 5 no intervalo X" não é uma linha, é uma
// CONDIÇÃO sobre linhas que ainda não existem. Aqui, "a notificação 42" É uma
// linha, discreta e existente. O SKIP LOCKED faz o segundo worker PULAR o que o
// primeiro já pegou, em vez de esperar — que é o que transforma N workers em
// paralelismo real em vez de uma fila serializada.
//
// O `OR (status = 'sending' AND claimed_at < ...)` é o visibility timeout:
// devolve para a fila o que um processo morto reivindicou e nunca concluiu.
const claimSQL = `
UPDATE notifications
SET status     = 'sending',
    claimed_at = now(),
    attempts   = attempts + 1
WHERE id IN (
    SELECT id
    FROM notifications
    WHERE status = 'pending'
       OR (status = 'sending' AND claimed_at < now() - make_interval(secs => $2::double precision))
    ORDER BY created_at
    FOR UPDATE SKIP LOCKED
    LIMIT $1
)
RETURNING id, reservation_id, kind, payload, attempts`

func (r *PostgresRepo) Claim(ctx context.Context, lote int, visibilidade time.Duration) ([]Notification, error) {
	rows, err := r.db.Query(ctx, claimSQL, lote, visibilidade.Seconds())
	if err != nil {
		return nil, fmt.Errorf("reivindicando notificações: %w", err)
	}
	defer rows.Close()

	fila := []Notification{}
	for rows.Next() {
		var n Notification
		var bruto []byte

		if err := rows.Scan(&n.ID, &n.ReservationID, &n.Kind, &bruto, &n.Attempts); err != nil {
			return nil, fmt.Errorf("lendo notificação: %w", err)
		}
		if err := json.Unmarshal(bruto, &n.Payload); err != nil {
			return nil, fmt.Errorf("payload inválido na notificação %s: %w", n.ID, err)
		}

		fila = append(fila, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reivindicando notificações: %w", err)
	}

	return fila, nil
}

const markSentSQL = `
UPDATE notifications
SET status = 'sent', sent_at = now(), claimed_at = NULL, last_error = NULL
WHERE id = $1`

func (r *PostgresRepo) MarkSent(ctx context.Context, id uuid.UUID) error {
	if _, err := r.db.Exec(ctx, markSentSQL, id); err != nil {
		return fmt.Errorf("marcando notificação %s como enviada: %w", id, err)
	}
	return nil
}

// O CASE é a máquina de estados da fila, e ela vive no BANCO, não em Go.
// Enquanto houver tentativa sobrando, a linha volta para 'pending' e alguém a
// pega de novo. Esgotadas, vira 'failed' — estado terminal, com o último erro
// gravado para alguém investigar.
//
// Isso é diferente do retry da Fase 1c de propósito: lá o retry é SÍNCRONO
// (o usuário está esperando a resposta HTTP, então três tentativas imediatas).
// Aqui é ASSÍNCRONO — ninguém está esperando, e a próxima tentativa pode ser
// daqui a minutos, depois de um restart, em outro processo.
const markFailedSQL = `
UPDATE notifications
SET status     = CASE WHEN attempts >= $2 THEN 'failed' ELSE 'pending' END,
    claimed_at = NULL,
    last_error = $3
WHERE id = $1`

func (r *PostgresRepo) MarkFailed(ctx context.Context, id uuid.UUID, maxTentativas int, causa error) error {
	if _, err := r.db.Exec(ctx, markFailedSQL, id, maxTentativas, causa.Error()); err != nil {
		return fmt.Errorf("marcando falha na notificação %s: %w", id, err)
	}
	return nil
}

// NotificationView é uma linha da fila exposta pela API de observação (débito #11:
// antes, uma notificação que esgotava as tentativas virava 'failed' e ninguém era
// avisado). É uma view separada do Notification interno do worker de propósito: o
// worker precisa do payload cru para reenviar; quem observa precisa do que
// IDENTIFICA a falha — a reserva, o tipo, quantas tentativas, e o último erro.
type NotificationView struct {
	ID            uuid.UUID `json:"id"`
	ReservationID uuid.UUID `json:"reservation_id"`
	Kind          Kind      `json:"kind"          example:"reservation_confirmed"`
	Status        string    `json:"status"        example:"failed"`
	Attempts      int       `json:"attempts"      example:"3"`
	LastError     string    `json:"last_error"    example:"sender: timeout"`
	CreatedAt     time.Time `json:"created_at"`
}

const listByStatusSQL = `
SELECT id, reservation_id, kind, status, attempts, coalesce(last_error, ''), created_at
FROM notifications
WHERE status = $1
ORDER BY created_at DESC`

// ListByStatus lê as notificações de um status para inspeção. Ordena da mais recente
// para a mais antiga: quem abre esta lista quer ver a última falha primeiro. Não
// pagina — para o volume de um restaurante, as 'failed' são poucas (e se um dia não
// forem, isso mesmo é o sinal de alarme que o débito #11 queria tornar visível).
func (r *PostgresRepo) ListByStatus(ctx context.Context, status string) ([]NotificationView, error) {
	rows, err := r.db.Query(ctx, listByStatusSQL, status)
	if err != nil {
		return nil, fmt.Errorf("listando notificações %q: %w", status, err)
	}
	defer rows.Close()

	out := []NotificationView{}
	for rows.Next() {
		var v NotificationView
		if err := rows.Scan(&v.ID, &v.ReservationID, &v.Kind, &v.Status, &v.Attempts, &v.LastError, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("lendo notificação: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listando notificações %q: %w", status, err)
	}
	return out, nil
}
