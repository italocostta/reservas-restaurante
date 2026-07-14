package reservation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"reservas-restaurante/internal/notification"
	"reservas-restaurante/internal/table"
)

// exclusion_violation: a constraint no_overlapping_reservations recusou o
// INSERT porque outra reserva confirmed já ocupa esse intervalo nessa mesa.
// Este é o ÚNICO lugar do projeto onde esse código aparece — daqui para cima
// ele já é ErrSlotTaken, e o allocator nunca precisa saber que existe Postgres.
const pgCodeExclusionViolation = "23P01"

// PostgresRepo implementa TableFinder e ReservationCreator (interfaces
// declaradas pelo allocator.go, o consumidor) e ainda serve o CRUD de reservas
// do handler. É o único arquivo do pacote que importa pgx e o único que conhece
// o schema de restaurant_tables — o pacote `table` não sabe que reservations
// existe, e é essa assimetria que mantém a organização por domínio de pé.
type PostgresRepo struct {
	db *pgxpool.Pool

	// Nome IANA do fuso do restaurante ("America/Sao_Paulo"). Necessário porque
	// timestamptz guarda um INSTANTE, não uma hora local: a reserva das 22h de
	// sábado em SP está gravada como domingo 01h UTC. Filtrar por "o dia" sem
	// converter para o fuso do restaurante devolve o dia errado.
	serviceTZ string
}

func NewPostgresRepo(db *pgxpool.Pool, serviceTZ *time.Location) *PostgresRepo {
	return &PostgresRepo{db: db, serviceTZ: serviceTZ.String()}
}

const getTableSQL = `
SELECT id, name, capacity, is_active, created_at
FROM restaurant_tables
WHERE id = $1`

// GetTable busca a mesa que o staff pediu explicitamente (caminho manual).
// Reaproveita table.ErrNotFound em vez de criar um erro próprio: é a mesma
// condição, e duplicar sentinela é como duplicar constante.
func (r *PostgresRepo) GetTable(ctx context.Context, id uuid.UUID) (table.Table, error) {
	var t table.Table

	err := r.db.QueryRow(ctx, getTableSQL, id).
		Scan(&t.ID, &t.Name, &t.Capacity, &t.IsActive, &t.CreatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return table.Table{}, table.ErrNotFound
	}
	if err != nil {
		return table.Table{}, fmt.Errorf("buscando mesa %s: %w", id, err)
	}

	return t, nil
}

// A heurística gulosa inteira mora nesta query: filtra capacidade, elimina as
// mesas com horário sobreposto, ordena da menor mesa suficiente para a maior.
// Ao allocator sobra escolher a primeira.
//
// NOT EXISTS e não LEFT JOIN ... IS NULL: os dois funcionam, mas o anti-join
// explícito não corre risco de multiplicar linhas se uma mesa tiver várias
// reservas, e o planner consegue parar na primeira reserva conflitante em vez
// de materializar todas.
//
// O `&&` bate exatamente no índice GIST parcial que a constraint
// no_overlapping_reservations criou (mesmo predicado, mesmo status='confirmed').
// O índice de integridade serve como índice de leitura, de graça.
//
// O desempate por t.name não é cosmético: sem ele, duas mesas de capacidade 4
// voltam em ordem arbitrária, a heurística vira não-determinística entre
// execuções, e um teste que hoje passa amanhã falha sem nada ter mudado.
const findCandidatesSQL = `
SELECT t.id, t.name, t.capacity, t.is_active, t.created_at
FROM restaurant_tables t
WHERE t.is_active = true
  AND t.capacity >= $1
  AND NOT EXISTS (
      SELECT 1
      FROM reservations r
      WHERE r.table_id = t.id
        AND r.status = 'confirmed'
        AND tstzrange(r.starts_at, r.ends_at) && tstzrange($2, $3)
  )
ORDER BY t.capacity ASC, t.name ASC`

func (r *PostgresRepo) FindCandidates(ctx context.Context, partySize int, starts, ends time.Time) ([]table.Table, error) {
	rows, err := r.db.Query(ctx, findCandidatesSQL, partySize, starts, ends)
	if err != nil {
		return nil, fmt.Errorf("buscando mesas candidatas: %w", err)
	}
	defer rows.Close()

	candidatas := []table.Table{}
	for rows.Next() {
		var t table.Table
		if err := rows.Scan(&t.ID, &t.Name, &t.Capacity, &t.IsActive, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("lendo mesa candidata: %w", err)
		}
		candidatas = append(candidatas, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("buscando mesas candidatas: %w", err)
	}

	return candidatas, nil
}

// `status` não é inserido: o DEFAULT 'confirmed' do schema é a única fonte da
// verdade para o estado inicial. Repetir o literal aqui criaria dois lugares
// para mudar quando o fluxo ganhar um status novo.
const insertSQL = `
INSERT INTO reservations
    (table_id, customer_name, customer_phone, party_size, starts_at, ends_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, status, created_at`

// Insert grava a reserva E o evento de notificação na MESMA transação. É o
// primeiro caso multi-statement do projeto — e o que finalmente justifica a
// "transação básica" que a terceira rodada de revisão da spec tinha removido do
// objetivo da Fase 1a, por não existir caso real que a pedisse.
//
// Sem a transação, os dois COMMIT seriam independentes: o processo morre entre
// eles e você fica ou com reserva sem notificação, ou — pior — com notificação
// de uma reserva que não existe.
func (r *PostgresRepo) Insert(ctx context.Context, res Reservation) (Reservation, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return Reservation{}, fmt.Errorf("abrindo transação: %w", err)
	}
	// Rollback incondicional no defer. Depois de um Commit bem-sucedido ele vira
	// no-op (devolve pgx.ErrTxClosed, que ignoramos de propósito). É o idioma de
	// Go para garantir que NENHUM caminho de saída — inclusive um panic — deixe
	// a transação aberta segurando locks até o timeout do servidor.
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, insertSQL,
		res.TableID,
		res.CustomerName,
		res.CustomerPhone,
		res.PartySize,
		res.StartsAt,
		res.EndsAt,
	).Scan(&res.ID, &res.Status, &res.CreatedAt)

	// A fronteira da Fase 1c: aqui o erro do driver vira erro de domínio, e o
	// allocator lá em cima decide o que fazer com ele sem nunca ver um pgconn.
	// O defer acima desfaz a transação abortada pelo 23P01.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgCodeExclusionViolation {
		return Reservation{}, ErrSlotTaken
	}
	if err != nil {
		return Reservation{}, fmt.Errorf("inserindo reserva: %w", err)
	}

	if err := notification.Enqueue(ctx, tx, res.ID, notification.KindConfirmed, payloadDe(res)); err != nil {
		return Reservation{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Reservation{}, fmt.Errorf("confirmando reserva: %w", err)
	}

	return res, nil
}

func payloadDe(res Reservation) notification.Payload {
	return notification.Payload{
		CustomerName:  res.CustomerName,
		CustomerPhone: res.CustomerPhone,
		PartySize:     res.PartySize,
		StartsAt:      res.StartsAt,
		EndsAt:        res.EndsAt,
	}
}

const colunas = `id, table_id, customer_name, customer_phone,
                 party_size, starts_at, ends_at, status, created_at`

func scanReservation(row pgx.Row) (Reservation, error) {
	var res Reservation
	err := row.Scan(
		&res.ID, &res.TableID, &res.CustomerName, &res.CustomerPhone,
		&res.PartySize, &res.StartsAt, &res.EndsAt, &res.Status, &res.CreatedAt,
	)
	return res, err
}

func (r *PostgresRepo) Get(ctx context.Context, id uuid.UUID) (Reservation, error) {
	res, err := scanReservation(
		r.db.QueryRow(ctx, `SELECT `+colunas+` FROM reservations WHERE id = $1`, id),
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, ErrNotFound
	}
	if err != nil {
		return Reservation{}, fmt.Errorf("buscando reserva %s: %w", id, err)
	}

	return res, nil
}

// ListFilter: nil em cada campo significa "não filtrar por isto". Mesmo
// tri-state do ?active= das mesas.
type ListFilter struct {
	Date    *string // YYYY-MM-DD, no fuso do restaurante
	TableID *uuid.UUID
	Status  *Status
}

// O AT TIME ZONE converte o instante (timestamptz) para a hora de parede do
// restaurante ANTES de extrair a data. Sem ele, o ::date usaria o fuso da
// sessão (UTC) e a reserva das 22h de sábado apareceria como domingo.
const listSQL = `
SELECT ` + colunas + `
FROM reservations
WHERE ($1::date IS NULL OR (starts_at AT TIME ZONE $4)::date = $1::date)
  AND ($2::uuid IS NULL OR table_id = $2)
  AND ($3::text IS NULL OR status   = $3)
ORDER BY starts_at`

func (r *PostgresRepo) List(ctx context.Context, f ListFilter) ([]Reservation, error) {
	rows, err := r.db.Query(ctx, listSQL, f.Date, f.TableID, f.Status, r.serviceTZ)
	if err != nil {
		return nil, fmt.Errorf("listando reservas: %w", err)
	}
	defer rows.Close()

	reservas := []Reservation{}
	for rows.Next() {
		res, err := scanReservation(rows)
		if err != nil {
			return nil, fmt.Errorf("lendo reserva: %w", err)
		}
		reservas = append(reservas, res)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listando reservas: %w", err)
	}

	return reservas, nil
}

// BusyWindows devolve as reservas confirmed da mesa que intersectam a janela.
//
// Sem AT TIME ZONE aqui, ao contrário do listSQL: quem converteu "o dia" em
// instantes foi o Go (Schedule.expedienteDe), então $2 e $3 já chegam como
// timestamptz. O SQL só compara instantes, que é o que ele faz bem.
//
// O predicado é IDÊNTICO ao do índice GIST parcial da constraint. Terceira vez
// que o índice de integridade serve como índice de leitura de graça.
const busyWindowsSQL = `
SELECT starts_at, ends_at
FROM reservations
WHERE table_id = $1
  AND status = 'confirmed'
  AND tstzrange(starts_at, ends_at) && tstzrange($2, $3)
ORDER BY starts_at`

func (r *PostgresRepo) BusyWindows(ctx context.Context, tableID uuid.UUID, from, to time.Time) ([]Window, error) {
	rows, err := r.db.Query(ctx, busyWindowsSQL, tableID, from, to)
	if err != nil {
		return nil, fmt.Errorf("buscando janelas ocupadas: %w", err)
	}
	defer rows.Close()

	ocupadas := []Window{}
	for rows.Next() {
		var w Window
		if err := rows.Scan(&w.StartsAt, &w.EndsAt); err != nil {
			return nil, fmt.Errorf("lendo janela ocupada: %w", err)
		}
		ocupadas = append(ocupadas, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("buscando janelas ocupadas: %w", err)
	}

	return ocupadas, nil
}

// Cancel é o soft delete: a linha fica, sai do índice parcial da EXCLUDE (que só
// indexa status='confirmed') e libera o horário.
//
// O `AND status = 'confirmed'` não é só um guarda — é um COMPARE-AND-SWAP. Sob
// dois cancelamentos concorrentes, o Postgres serializa pelo lock da linha: o
// segundo UPDATE espera, reavalia o WHERE depois do commit do primeiro, vê que o
// status já não é 'confirmed', e afeta ZERO linhas. Exatamente uma notificação é
// enfileirada, sem lock explícito e sem retry.
//
// Sem essa cláusula, o endpoint continuaria idempotente (204 nas duas vezes) mas
// o EFEITO COLATERAL não: o cliente receberia dois SMS de cancelamento.
// Idempotência do endpoint não é idempotência do efeito colateral.
const cancelSQL = `
UPDATE reservations
SET status = 'cancelled'
WHERE id = $1 AND status = 'confirmed'
RETURNING id, customer_name, customer_phone, party_size, starts_at, ends_at`

const statusDaReservaSQL = `SELECT status FROM reservations WHERE id = $1`

func (r *PostgresRepo) Cancel(ctx context.Context, id uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrindo transação: %w", err)
	}
	defer tx.Rollback(ctx)

	var res Reservation
	err = tx.QueryRow(ctx, cancelSQL, id).Scan(
		&res.ID, &res.CustomerName, &res.CustomerPhone,
		&res.PartySize, &res.StartsAt, &res.EndsAt,
	)

	// Zero linhas afetadas. Duas causas possíveis, com respostas HTTP diferentes.
	if errors.Is(err, pgx.ErrNoRows) {
		return r.explicarSemMudanca(ctx, tx, id)
	}
	if err != nil {
		return fmt.Errorf("cancelando reserva %s: %w", id, err)
	}

	if err := notification.Enqueue(ctx, tx, res.ID, notification.KindCancelled, payloadDe(res)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("confirmando cancelamento de %s: %w", id, err)
	}

	return nil
}

// explicarSemMudanca separa "a reserva não existe" (→ 404) de "já estava
// cancelada" (→ 204, idempotente e SEM nova notificação).
func (r *PostgresRepo) explicarSemMudanca(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var status Status

	err := tx.QueryRow(ctx, statusDaReservaSQL, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("verificando reserva %s: %w", id, err)
	}

	// Já cancelada: sucesso, sem efeito. Não há o que commitar — o defer
	// desfaz a transação vazia.
	return nil
}
