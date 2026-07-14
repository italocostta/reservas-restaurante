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

// GetTable busca uma mesa. Usada pela agenda (ScheduleReader), que sempre
// pergunta por uma só.
//
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

const getTablesSQL = `
SELECT id, name, capacity, is_active, created_at
FROM restaurant_tables
WHERE id = ANY($1)`

// GetTables busca N mesas de uma vez — o caminho manual da Fase 3a pode pedir
// uma combinação de várias, e N chamadas a GetTable seriam N round-trips para
// responder uma pergunta só.
//
// NÃO devolve ErrNotFound quando algum id não existe: devolve as que achou. Quem
// sabe o que fazer com "pedi 3 e vieram 2" é o allocator, que consegue montar
// uma mensagem útil dizendo QUAL mesa não existe. O repositório informa; ele não
// julga.
func (r *PostgresRepo) GetTables(ctx context.Context, ids []uuid.UUID) ([]table.Table, error) {
	rows, err := r.db.Query(ctx, getTablesSQL, ids)
	if err != nil {
		return nil, fmt.Errorf("buscando mesas: %w", err)
	}
	defer rows.Close()

	mesas := []table.Table{}
	for rows.Next() {
		var t table.Table
		if err := rows.Scan(&t.ID, &t.Name, &t.Capacity, &t.IsActive, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("lendo mesa: %w", err)
		}
		mesas = append(mesas, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("buscando mesas: %w", err)
	}

	return mesas, nil
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
// Fase 3a: o NOT EXISTS agora olha reservation_tables, não reservations. A
// pergunta "esta mesa está ocupada neste intervalo?" mudou de lugar junto com a
// constraint — uma mesa pode estar ocupada por ser METADE de uma combinação, e
// isso não aparece mais em reservations.table_id.
const findCandidatesSQL = `
SELECT t.id, t.name, t.capacity, t.is_active, t.created_at
FROM restaurant_tables t
WHERE t.is_active = true
  AND t.capacity >= $1
  AND NOT EXISTS (
      SELECT 1
      FROM reservation_tables rt
      WHERE rt.table_id = t.id
        AND rt.status = 'confirmed'
        AND tstzrange(rt.starts_at, rt.ends_at) && tstzrange($2, $3)
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
//
// `table_id` também sumiu: a coluna ainda existe (a 0007 é que a derruba), mas
// fica NULL. Quem guarda as mesas agora é reservation_tables.
const insertSQL = `
INSERT INTO reservations
    (customer_name, customer_phone, party_size, starts_at, ends_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, status, created_at`

// unnest transforma o array de uuid em N linhas: uma reserva de 3 mesas vira 3
// linhas de junção num ÚNICO statement, não três round-trips.
//
// É AQUI que a EXCLUDE dispara agora. Numa combinação, o INSERT pode falhar por
// causa da terceira mesa depois de as duas primeiras já terem entrado — e a
// transação desfaz tudo. Sem ela, você teria uma reserva "meio confirmada",
// ocupando duas mesas e faltando uma.
const insertTablesSQL = `
INSERT INTO reservation_tables (reservation_id, table_id, starts_at, ends_at, status)
SELECT $1, unnest($2::uuid[]), $3, $4, $5`

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
		res.CustomerName,
		res.CustomerPhone,
		res.PartySize,
		res.StartsAt,
		res.EndsAt,
	).Scan(&res.ID, &res.Status, &res.CreatedAt)
	if err != nil {
		return Reservation{}, fmt.Errorf("inserindo reserva: %w", err)
	}

	// A fronteira da Fase 1c, que agora mora aqui: a EXCLUDE mudou de tabela,
	// mas o código de erro é o MESMO (23P01). Toda a máquina do allocator —
	// ErrSlotTaken, retry no automático, ErrTableUnavailable no manual —
	// continua valendo sem uma linha de alteração.
	_, err = tx.Exec(ctx, insertTablesSQL,
		res.ID, res.TableIDs, res.StartsAt, res.EndsAt, res.Status)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgCodeExclusionViolation {
		return Reservation{}, ErrSlotTaken
	}
	if err != nil {
		return Reservation{}, fmt.Errorf("associando mesas à reserva: %w", err)
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

// A reserva e suas mesas vêm numa consulta só: o array_agg colapsa as linhas de
// junção numa coluna uuid[].
//
// O FILTER + coalesce não são firula. Sem eles, uma reserva sem linha de junção
// (que não deveria existir, mas o banco não impede) produziria `{NULL}` — um
// array com um elemento nulo — em vez de `{}`. É a MESMA armadilha do `[]` vs
// `null` do JSON, agora em SQL: o vazio precisa ser explicitamente vazio.
const selectReservas = `
SELECT r.id,
       coalesce(array_agg(rt.table_id ORDER BY rt.table_id)
                FILTER (WHERE rt.table_id IS NOT NULL), '{}') AS table_ids,
       r.customer_name, r.customer_phone, r.party_size,
       r.starts_at, r.ends_at, r.status, r.created_at
FROM reservations r
LEFT JOIN reservation_tables rt ON rt.reservation_id = r.id`

func scanReservation(row pgx.Row) (Reservation, error) {
	var res Reservation
	err := row.Scan(
		&res.ID, &res.TableIDs, &res.CustomerName, &res.CustomerPhone,
		&res.PartySize, &res.StartsAt, &res.EndsAt, &res.Status, &res.CreatedAt,
	)
	return res, err
}

func (r *PostgresRepo) Get(ctx context.Context, id uuid.UUID) (Reservation, error) {
	res, err := scanReservation(
		r.db.QueryRow(ctx, selectReservas+` WHERE r.id = $1 GROUP BY r.id`, id),
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
//
// ARMADILHA da Fase 3a: o filtro por mesa usa EXISTS, e não `rt.table_id = $2`.
// Se ele filtrasse direto no LEFT JOIN, o array_agg veria SÓ a mesa filtrada — e
// uma reserva combinada nas mesas A e B, consultada por `?table_id=A`, voltaria
// dizendo que ocupa apenas a mesa A. A resposta estaria errada, e ninguém
// perceberia, porque ela é plausível. O EXISTS filtra QUAIS RESERVAS aparecem sem
// mexer em QUAIS MESAS cada uma reporta.
const listSQL = selectReservas + `
WHERE ($1::date IS NULL OR (r.starts_at AT TIME ZONE $4)::date = $1::date)
  AND ($2::uuid IS NULL OR EXISTS (
        SELECT 1 FROM reservation_tables f
        WHERE f.reservation_id = r.id AND f.table_id = $2))
  AND ($3::text IS NULL OR r.status = $3)
GROUP BY r.id
ORDER BY r.starts_at`

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
// Fase 3a: lê reservation_tables. Uma mesa pode estar ocupada por ser metade de
// uma combinação — e essa ocupação só existe na tabela de junção.
const busyWindowsSQL = `
SELECT starts_at, ends_at
FROM reservation_tables
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

const listActiveTablesSQL = `
SELECT id, name, capacity, is_active, created_at
FROM restaurant_tables
WHERE is_active = true
ORDER BY name`

// ListActiveTables devolve o salão: as mesas que a grade do dia precisa mostrar.
//
// Não reusa table.PostgresRepo.List(active=true), e isso é deliberado — é o mesmo
// princípio que já justificou o GetTable existir aqui em vez de o Schedule
// importar o pacote table: quem consome define a interface. Fazer o Schedule
// depender de table.PostgresRepo religaria os dois domínios que a seção 4 da spec
// separou de propósito, para economizar uma query de cinco linhas.
func (r *PostgresRepo) ListActiveTables(ctx context.Context) ([]table.Table, error) {
	rows, err := r.db.Query(ctx, listActiveTablesSQL)
	if err != nil {
		return nil, fmt.Errorf("listando mesas ativas: %w", err)
	}
	defer rows.Close()

	mesas := []table.Table{}
	for rows.Next() {
		var t table.Table
		if err := rows.Scan(&t.ID, &t.Name, &t.Capacity, &t.IsActive, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("lendo mesa ativa: %w", err)
		}
		mesas = append(mesas, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listando mesas ativas: %w", err)
	}

	return mesas, nil
}

// Mesmo predicado do busyWindowsSQL, sem o `table_id = $1`: as ocupações de TODAS
// as mesas na janela, de uma vez.
//
// O ORDER BY table_id, starts_at não é cosmético. O janelasLivres é um sweep com
// cursor: ele EXIGE as ocupadas ordenadas por início, e a documentação dele diz
// "o ORDER BY do SQL garante isso". Aqui a garantia precisa valer por mesa, não
// só globalmente — daí as duas colunas. Trocar por um ORDER BY só de table_id
// devolveria janelas livres erradas, em silêncio.
const busyWindowsAllSQL = `
SELECT table_id, starts_at, ends_at
FROM reservation_tables
WHERE status = 'confirmed'
  AND tstzrange(starts_at, ends_at) && tstzrange($1, $2)
ORDER BY table_id, starts_at`

// BusyWindowsAll é o BusyWindows sem o recorte por mesa: uma query para o salão
// inteiro, em vez de uma por mesa.
//
// É o motivo de este endpoint existir. Montar a grade do dia chamando
// BusyWindows N vezes seria um N+1 clássico — 20 mesas, 20 round-trips, a cada
// vez que o staff mexesse no horário na tela. O banco responde a pergunta inteira
// de uma vez porque ela É uma pergunta só.
//
// Devolve map e não slice porque o consumidor (o DayGrid) vai iterar as MESAS e
// perguntar "quais as ocupadas desta?" — e o zero value do map já responde a mesa
// sem nenhuma reserva: `ocupadas[id]` num id ausente devolve nil, e
// `janelasLivres(expediente, nil)` devolve o expediente inteiro livre. A mesa
// vazia não precisa de caso especial em lugar nenhum.
func (r *PostgresRepo) BusyWindowsAll(ctx context.Context, from, to time.Time) (map[uuid.UUID][]Window, error) {
	rows, err := r.db.Query(ctx, busyWindowsAllSQL, from, to)
	if err != nil {
		return nil, fmt.Errorf("buscando janelas ocupadas do salão: %w", err)
	}
	defer rows.Close()

	porMesa := map[uuid.UUID][]Window{}
	for rows.Next() {
		var id uuid.UUID
		var w Window
		if err := rows.Scan(&id, &w.StartsAt, &w.EndsAt); err != nil {
			return nil, fmt.Errorf("lendo janela ocupada do salão: %w", err)
		}
		porMesa[id] = append(porMesa[id], w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("buscando janelas ocupadas do salão: %w", err)
	}

	return porMesa, nil
}

// `ends_at > now()` e não `starts_at > now()`: a reserva que está ACONTECENDO
// agora — entrou às 19h, sai às 21h, e são 20h — é a mais grave de todas para
// desativar a mesa embaixo. Filtrar por starts_at deixaria passar exatamente o
// caso em que há gente sentada.
//
// O now() é do BANCO, e não um parâmetro vindo do Clock injetável. Aqui isso é
// certo: o Clock existe para que a VALIDAÇÃO de "starts_at no passado" seja
// testável sem datas que expiram (seção 3 da spec). Esta contagem não é validação
// de entrada do usuário — é uma leitura do estado do mundo, e o relógio do mundo é
// o do banco.
const contarReservasFuturasSQL = `
SELECT count(*)
FROM reservation_tables
WHERE table_id = $1
  AND status = 'confirmed'
  AND ends_at > now()`

// ContarReservasFuturas responde a UMA pergunta, feita por OUTRO domínio: "posso
// desativar esta mesa?".
//
// O pacote `table` não sabe que reservas existem — é a assimetria que sustenta a
// organização por domínio (seção 6). Mas a restrição vem de reservas. A saída é a
// mesma de sempre neste projeto: `table` DECLARA a interface de que precisa, sem
// nomear este pacote, e o main.go costura. Ninguém importa ninguém.
//
// Lê reservation_tables e não reservations: uma mesa pode estar ocupada por ser
// METADE de uma combinação (Fase 3a), e essa ocupação só existe na tabela de
// junção.
func (r *PostgresRepo) ContarReservasFuturas(ctx context.Context, tableID uuid.UUID) (int, error) {
	var n int

	if err := r.db.QueryRow(ctx, contarReservasFuturasSQL, tableID).Scan(&n); err != nil {
		return 0, fmt.Errorf("contando reservas futuras da mesa %s: %w", tableID, err)
	}

	return n, nil
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
