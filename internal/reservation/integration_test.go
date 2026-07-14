package reservation

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	_ "time/tzdata"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Grupo grande o bastante para que NENHUMA mesa real do banco sirva: o
// FindCandidates filtra por `capacity >= $1`, e só a mesa criada por este teste
// tem capacidade suficiente. É o que isola o teste sem exigir banco separado.
const capacidadeDeTeste = 999

// poolDeTeste conecta no banco real. Sem TEST_DATABASE_URL, o teste é PULADO —
// `go test ./...` segue verde numa máquina sem banco, e ninguém aponta para
// produção por acidente ao reusar DATABASE_URL.
func poolDeTeste(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL não definida — pulando teste de integração")
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("abrindo pool: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("conectando no Postgres: %v", err)
	}

	return pool
}

// mesaDeTeste cria uma mesa com nome único e agenda a limpeza. O nome é único
// porque `-count=20` roda o teste 20 vezes contra o mesmo banco.
func mesaDeTeste(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	// Varre restos de execuções que morreram antes do Cleanup. Sem isto, uma
	// mesa órfã de capacidade 999 vira uma SEGUNDA candidata e o teste passa a
	// aceitar dois sucessos — falhando por um motivo que não é o código.
	limparMesasDeTeste(ctx, pool)

	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO restaurant_tables (name, capacity) VALUES ($1, $2) RETURNING id`,
		"teste-"+uuid.NewString(), capacidadeDeTeste,
	).Scan(&id)
	if err != nil {
		t.Fatalf("criando mesa de teste: %v", err)
	}

	t.Cleanup(func() { limparMesasDeTeste(context.Background(), pool) })

	return id
}

func limparMesasDeTeste(ctx context.Context, pool *pgxpool.Pool) {
	pool.Exec(ctx, `
		DELETE FROM reservations
		WHERE table_id IN (SELECT id FROM restaurant_tables WHERE name LIKE 'teste-%')`)
	pool.Exec(ctx, `DELETE FROM restaurant_tables WHERE name LIKE 'teste-%'`)
}

func horasDeTeste() ServiceHours {
	return ServiceHours{Start: 18 * time.Hour, End: 23 * time.Hour, TZ: fusoSP}
}

// Janela sempre no futuro (a validação #3 rejeita passado) e sempre dentro do
// expediente (validação #8): 19h, daqui a 30 dias, no fuso do restaurante.
func janelaFutura() (time.Time, time.Time) {
	d := time.Now().In(fusoSP).AddDate(0, 0, 30)
	inicio := time.Date(d.Year(), d.Month(), d.Day(), 19, 0, 0, 0, fusoSP)
	return inicio, inicio.Add(2 * time.Hour)
}

// disparar roda n goroutines simultâneas e devolve o erro de cada uma.
func disparar(n int, fn func() error) []error {
	erros := make([]error, n)

	var largada sync.WaitGroup // segura todas até a última estar pronta
	var fim sync.WaitGroup

	largada.Add(1)
	fim.Add(n)

	for i := range n {
		// Go 1.22+: `i` é uma variável NOVA a cada iteração. Antes disso, todas
		// as goroutines compartilhavam o mesmo `i` e escreviam no mesmo índice —
		// o bug de closure mais famoso da linguagem, corrigido na 1.22.
		go func() {
			defer fim.Done()
			largada.Wait() // todas bloqueiam aqui
			erros[i] = fn()
		}()
	}

	// Solta todas de uma vez: maximiza a chance de elas realmente colidirem, em
	// vez de a primeira já ter terminado quando a última nasce.
	largada.Done()
	fim.Wait()

	return erros
}

func contarConfirmadas(t *testing.T, pool *pgxpool.Pool, tableID uuid.UUID) int {
	t.Helper()

	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM reservations WHERE table_id = $1 AND status = 'confirmed'`,
		tableID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("contando reservas: %v", err)
	}

	return n
}

// N goroutines disputam a única mesa que comporta o grupo, sem informar table_id.
// A EXCLUDE derruba as perdedoras com 23P01; o retry reconsulta, não acha mais
// candidata, e devolve ErrNoAvailability.
func TestConcorrenciaCaminhoAutomatico(t *testing.T) {
	pool := poolDeTeste(t)
	tableID := mesaDeTeste(t, pool)

	repo := NewPostgresRepo(pool, fusoSP)
	alloc := NewAllocator(repo, repo, horasDeTeste(), SystemClock{})
	inicio, fim := janelaFutura()

	const n = 10
	erros := disparar(n, func() error {
		_, err := alloc.CreateReservation(context.Background(), AllocationRequest{
			CustomerName:  "Cliente",
			CustomerPhone: "11999998888",
			PartySize:     capacidadeDeTeste,
			StartsAt:      inicio,
			EndsAt:        fim,
		})
		return err
	})

	conferir(t, erros, ErrNoAvailability)

	if confirmadas := contarConfirmadas(t, pool, tableID); confirmadas != 1 {
		t.Errorf("%d reservas confirmed no banco, quero exatamente 1 — a EXCLUDE falhou", confirmadas)
	}
}

// Mesmo cenário, mas todas pedindo a MESMA mesa explicitamente. A colisão é
// definitiva: sem retry, e a mensagem tem que dizer a verdade (ErrTableUnavailable,
// não ErrNoAvailability).
func TestConcorrenciaCaminhoManual(t *testing.T) {
	pool := poolDeTeste(t)
	tableID := mesaDeTeste(t, pool)

	repo := NewPostgresRepo(pool, fusoSP)
	alloc := NewAllocator(repo, repo, horasDeTeste(), SystemClock{})
	inicio, fim := janelaFutura()

	const n = 10
	erros := disparar(n, func() error {
		_, err := alloc.CreateReservation(context.Background(), AllocationRequest{
			PreferredTableID: &tableID,
			CustomerName:     "Cliente",
			CustomerPhone:    "11999998888",
			PartySize:        capacidadeDeTeste,
			StartsAt:         inicio,
			EndsAt:           fim,
		})
		return err
	})

	conferir(t, erros, ErrTableUnavailable)

	if confirmadas := contarConfirmadas(t, pool, tableID); confirmadas != 1 {
		t.Errorf("%d reservas confirmed no banco, quero exatamente 1 — a EXCLUDE falhou", confirmadas)
	}
}

// conferir exige: exatamente 1 sucesso, todos os demais com o erro esperado,
// nenhum erro genérico, e ErrSlotTaken nunca vazando.
func conferir(t *testing.T, erros []error, esperado error) {
	t.Helper()

	sucessos := 0
	for i, err := range erros {
		switch {
		case err == nil:
			sucessos++

		case errors.Is(err, ErrSlotTaken):
			t.Errorf("goroutine %d: ErrSlotTaken vazou do allocator — é sinal interno, o handler nunca deve vê-lo", i)

		case errors.Is(err, esperado):
			// esperado

		default:
			t.Errorf("goroutine %d: erro inesperado %v (%T) — quero %v", i, err, err, esperado)
		}
	}

	if sucessos != 1 {
		t.Fatalf("%d sucessos, quero exatamente 1 — a corrida não foi resolvida", sucessos)
	}
}
