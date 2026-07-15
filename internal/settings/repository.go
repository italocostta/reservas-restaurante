package settings

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"reservas-restaurante/internal/reservation"
)

// PostgresRepo lê e escreve a config do restaurante. É o único arquivo do pacote
// que importa pgx.
type PostgresRepo struct {
	db *pgxpool.Pool
}

func NewPostgresRepo(db *pgxpool.Pool) *PostgresRepo {
	return &PostgresRepo{db: db}
}

// HorasVigentes e AbertoEm satisfazem a reservation.ExpedienteVigente — a
// interface que o allocator e a agenda declaram. É por elas que a config editável
// deste pacote chega ao domínio de reservas SEM que reservation importe settings.
//
// Cada chamada relê do banco. Poderia haver cache, mas a config muda raramente e
// é lida uma vez por reserva/consulta — o custo é um SELECT numa tabela de uma
// linha, e um cache traria o problema de invalidá-lo quando o staff editar.

func (r *PostgresRepo) HorasVigentes(ctx context.Context) (reservation.ServiceHours, error) {
	s, err := r.Load(ctx)
	if err != nil {
		return reservation.ServiceHours{}, err
	}
	return s.Hours, nil
}

func (r *PostgresRepo) AbertoEm(ctx context.Context, dia string) (bool, error) {
	s, err := r.Load(ctx)
	if err != nil {
		return false, err
	}
	excecoes, err := r.Excecoes(ctx)
	if err != nil {
		return false, err
	}
	return AbertoEm(s, excecoes, dia)
}

const selectSettingsSQL = `
SELECT service_start, service_end, service_tz, open_weekdays
FROM restaurant_settings
WHERE id = 1`

// Load lê a config vigente. O horário vem como `time` do Postgres, que o pgx
// entrega como time.Time num dia-base fictício — extraímos só a duração desde a
// meia-noite, que é o que ServiceHours guarda.
func (r *PostgresRepo) Load(ctx context.Context) (Settings, error) {
	var start, end time.Time
	var tzNome string
	var dias []int16

	err := r.db.QueryRow(ctx, selectSettingsSQL).Scan(&start, &end, &tzNome, &dias)
	if err != nil {
		return Settings{}, fmt.Errorf("lendo settings: %w", err)
	}

	tz, err := time.LoadLocation(tzNome)
	if err != nil {
		return Settings{}, fmt.Errorf("fuso inválido no banco (%q): %w", tzNome, err)
	}

	abertos := make(map[time.Weekday]bool, len(dias))
	for _, d := range dias {
		abertos[time.Weekday(d)] = true
	}

	return Settings{
		Hours: reservation.ServiceHours{
			Start: desdeMeiaNoite(start),
			End:   desdeMeiaNoite(end),
			TZ:    tz,
		},
		OpenWeekdays: abertos,
	}, nil
}

func desdeMeiaNoite(t time.Time) time.Duration {
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
}

const updateSettingsSQL = `
UPDATE restaurant_settings
SET service_start = $1, service_end = $2, service_tz = $3, open_weekdays = $4
WHERE id = 1`

// Save grava a config inteira. As constraints da 0009 (expediente_coerente,
// dias_validos) são a rede de segurança: horário invertido ou dia da semana
// inexistente batem no banco e voltam como erro, não passam.
func (r *PostgresRepo) Save(ctx context.Context, s Settings) error {
	dias := make([]int16, 0, len(s.OpenWeekdays))
	for d, aberto := range s.OpenWeekdays {
		if aberto {
			dias = append(dias, int16(d))
		}
	}

	_, err := r.db.Exec(ctx, updateSettingsSQL,
		formatarDuracao(s.Hours.Start),
		formatarDuracao(s.Hours.End),
		s.Hours.TZ.String(),
		dias,
	)
	if err != nil {
		return fmt.Errorf("gravando settings: %w", err)
	}
	return nil
}

// formatarDuracao volta de duração para "HH:MM", o formato que a coluna `time`
// aceita. O ida-e-volta com desdeMeiaNoite é o mesmo do config: o domínio quer
// aritmética, o banco quer um horário.
func formatarDuracao(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(d.Hours()), int(d.Minutes())%60)
}

const selectExcecoesSQL = `
SELECT day, is_open, coalesce(note, '')
FROM service_exceptions
ORDER BY day`

// Excecoes devolve TODAS as exceções, indexadas por dia, para o AbertoEm consultar
// em O(1). São poucas (feriados de um ano), então trazer todas é mais barato que
// uma query por dia consultado.
func (r *PostgresRepo) Excecoes(ctx context.Context) (map[string]Exception, error) {
	rows, err := r.db.Query(ctx, selectExcecoesSQL)
	if err != nil {
		return nil, fmt.Errorf("lendo exceções: %w", err)
	}
	defer rows.Close()

	out := map[string]Exception{}
	for rows.Next() {
		var dia time.Time
		var ex Exception
		if err := rows.Scan(&dia, &ex.IsOpen, &ex.Note); err != nil {
			return nil, fmt.Errorf("lendo exceção: %w", err)
		}
		ex.Day = dia.Format(time.DateOnly)
		out[ex.Day] = ex
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lendo exceções: %w", err)
	}
	return out, nil
}

// ListExcecoes devolve as exceções como slice ordenado, para a API. O map do
// AbertoEm é para consulta; a lista é para exibição.
func (r *PostgresRepo) ListExcecoes(ctx context.Context) ([]Exception, error) {
	m, err := r.Excecoes(ctx)
	if err != nil {
		return nil, err
	}
	// Reordena por dia — o map perdeu a ordem do ORDER BY. Comparação lexical de
	// string funciona aqui porque AAAA-MM-DD ordena igual à data.
	out := make([]Exception, 0, len(m))
	for _, ex := range m {
		out = append(out, ex)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, nil
}

const upsertExcecaoSQL = `
INSERT INTO service_exceptions (day, is_open, note)
VALUES ($1, $2, $3)
ON CONFLICT (day) DO UPDATE SET is_open = $2, note = $3`

// SaveExcecao insere ou atualiza. Upsert e não INSERT: marcar a mesma data duas
// vezes é corrigir a decisão, não um erro — a segunda marcação vence.
func (r *PostgresRepo) SaveExcecao(ctx context.Context, ex Exception) error {
	var nota *string
	if ex.Note != "" {
		nota = &ex.Note
	}
	if _, err := r.db.Exec(ctx, upsertExcecaoSQL, ex.Day, ex.IsOpen, nota); err != nil {
		return fmt.Errorf("gravando exceção %s: %w", ex.Day, err)
	}
	return nil
}

const deleteExcecaoSQL = `DELETE FROM service_exceptions WHERE day = $1`

// DeleteExcecao é idempotente: apagar uma exceção que não existe devolve nil. A
// data volta a seguir a regra semanal, que é o efeito desejado nas duas hipóteses
// ("apaguei" e "já não estava lá").
func (r *PostgresRepo) DeleteExcecao(ctx context.Context, dia string) error {
	if _, err := r.db.Exec(ctx, deleteExcecaoSQL, dia); err != nil {
		return fmt.Errorf("removendo exceção %s: %w", dia, err)
	}
	return nil
}
