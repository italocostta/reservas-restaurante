package table

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRepo faz só CRUD de restaurant_tables. Não conhece reservations:
// a query de disponibilidade (JOIN com reservations) mora em
// reservation/repository.go, porque "quais mesas estão livres numa janela"
// é pergunta do domínio de reservas.
type PostgresRepo struct {
	db *pgxpool.Pool
}

func NewPostgresRepo(db *pgxpool.Pool) *PostgresRepo {
	return &PostgresRepo{db: db}
}

const createSQL = `
INSERT INTO restaurant_tables (name, capacity)
VALUES ($1, $2)
RETURNING id, name, capacity, is_active, created_at`

func (r *PostgresRepo) Create(ctx context.Context, name string, capacity int) (Table, error) {
	var t Table
	err := r.db.QueryRow(ctx, createSQL, name, capacity).
		Scan(&t.ID, &t.Name, &t.Capacity, &t.IsActive, &t.CreatedAt)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Table{}, ErrDuplicateName
	}
	if err != nil {
		return Table{}, fmt.Errorf("criando mesa: %w", err)
	}
	return t, nil
}

const listSQL = `
SELECT id, name, capacity, is_active, created_at
FROM restaurant_tables
WHERE ($1::boolean IS NULL OR is_active = $1)
ORDER BY name`

// List devolve as mesas, opcionalmente filtradas por is_active.
// active é ponteiro porque são três pedidos distintos: nil = sem filtro,
// &true = só ativas, &false = só inativas. Um bool só distinguiria dois.
func (r *PostgresRepo) List(ctx context.Context, active *bool) ([]Table, error) {
	rows, err := r.db.Query(ctx, listSQL, active)
	if err != nil {
		return nil, fmt.Errorf("listando mesas: %w", err)
	}
	defer rows.Close()

	tables := []Table{}
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.ID, &t.Name, &t.Capacity, &t.IsActive, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("lendo mesa: %w", err)
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listando mesas: %w", err)
	}
	return tables, nil
}

const getByIDSQL = `
SELECT id, name, capacity, is_active, created_at
FROM restaurant_tables
WHERE id = $1`

func (r *PostgresRepo) GetByID(ctx context.Context, id uuid.UUID) (Table, error) {
	var t Table
	err := r.db.QueryRow(ctx, getByIDSQL, id).
		Scan(&t.ID, &t.Name, &t.Capacity, &t.IsActive, &t.CreatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return Table{}, ErrNotFound
	}
	if err != nil {
		return Table{}, fmt.Errorf("buscando mesa %s: %w", id, err)
	}
	return t, nil
}

// UpdateParams carrega só os campos que o PATCH pode alterar. Ponteiro nil
// significa "não mexer neste campo" — distinto de "mudar para o zero value",
// que um valor não-ponteiro não conseguiria expressar (IsActive: false).
type UpdateParams struct {
	Name     *string
	Capacity *int
	IsActive *bool
}

const updateSQL = `
UPDATE restaurant_tables
SET name      = COALESCE($2::text, name),
    capacity  = COALESCE($3::smallint, capacity),
    is_active = COALESCE($4::boolean, is_active)
WHERE id = $1
RETURNING id, name, capacity, is_active, created_at`

func (r *PostgresRepo) Update(ctx context.Context, id uuid.UUID, p UpdateParams) (Table, error) {
	var t Table
	err := r.db.QueryRow(ctx, updateSQL, id, p.Name, p.Capacity, p.IsActive).
		Scan(&t.ID, &t.Name, &t.Capacity, &t.IsActive, &t.CreatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return Table{}, ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Table{}, ErrDuplicateName
	}
	if err != nil {
		return Table{}, fmt.Errorf("atualizando mesa %s: %w", id, err)
	}
	return t, nil
}
