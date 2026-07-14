package notification

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeQueue struct {
	mu sync.Mutex

	pendentes    []Notification
	claims       int
	segundoClaim chan struct{} // fecha na 2ª chamada — ver comentário no teste

	enviadas   []uuid.UUID
	falhadas   []uuid.UUID
	maxVisto   int
	errosDeCtx []error // ctx.Err() no instante de CADA marcação
}

func (q *fakeQueue) Claim(_ context.Context, _ int, _ time.Duration) ([]Notification, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.claims++
	if q.claims == 2 {
		close(q.segundoClaim)
	}
	if q.claims > 1 {
		return nil, nil
	}

	return q.pendentes, nil
}

func (q *fakeQueue) MarkSent(ctx context.Context, id uuid.UUID) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.enviadas = append(q.enviadas, id)
	q.errosDeCtx = append(q.errosDeCtx, ctx.Err())
	return nil
}

func (q *fakeQueue) MarkFailed(ctx context.Context, id uuid.UUID, max int, _ error) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.falhadas = append(q.falhadas, id)
	q.maxVisto = max
	q.errosDeCtx = append(q.errosDeCtx, ctx.Err())
	return nil
}

type fakeSender struct {
	demora time.Duration
	erro   error

	mu       sync.Mutex
	enviadas []uuid.UUID
}

func (s *fakeSender) Send(_ context.Context, n Notification) error {
	time.Sleep(s.demora)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.enviadas = append(s.enviadas, n.ID)

	return s.erro
}

func pendentes(n int) []Notification {
	fila := make([]Notification, n)
	for i := range fila {
		fila[i] = Notification{ID: uuid.New(), Kind: KindConfirmed, Attempts: 1}
	}
	return fila
}

func rodar(t *testing.T, q *fakeQueue, s *fakeSender, lote int) {
	t.Helper()

	cfg := DefaultConfig()
	cfg.Workers = 2
	cfg.Batch = lote
	cfg.PollInterval = time.Hour // o teste controla o ciclo, não o relógio

	ctx, cancelar := context.WithCancel(context.Background())

	parou := make(chan struct{})
	go func() {
		defer close(parou)
		NewDispatcher(q, s, cfg).Run(ctx)
	}()

	// A 2ª chamada de Claim só acontece DEPOIS de o poller ter empurrado o lote
	// inteiro para o channel (o `continue` do lote cheio). É a garantia
	// determinística de que existe trabalho em voo no momento do cancelamento —
	// sem ela, o teste seria uma aposta no escalonador.
	<-q.segundoClaim

	cancelar() // o Ctrl+C, no meio do envio

	select {
	case <-parou:
	case <-time.After(10 * time.Second):
		t.Fatal("Run não retornou — o shutdown travou")
	}
}

// O teste que justifica o context.WithoutCancel.
//
// No shutdown, o ctx do Run está cancelado, mas os workers ainda estão drenando
// o channel. Se eles usassem esse ctx para falar com o banco, TODA notificação
// enviada durante o encerramento falharia ao ser marcada — e seria reenviada no
// próximo boot, quando o visibility timeout a devolvesse à fila.
//
// Se alguém trocar `context.WithoutCancel(ctx)` por `ctx` em despachar(), este
// teste fica vermelho na asserção de ctx.Err().
func TestShutdownDrenaTudoQueEstaEmVoo(t *testing.T) {
	const n = 8

	q := &fakeQueue{pendentes: pendentes(n), segundoClaim: make(chan struct{})}
	s := &fakeSender{demora: 30 * time.Millisecond}

	rodar(t, q, s, n)

	if len(s.enviadas) != n {
		t.Errorf("%d notificações enviadas, quero %d — o shutdown descartou trabalho em voo",
			len(s.enviadas), n)
	}
	if len(q.enviadas) != n {
		t.Errorf("%d notificações marcadas como enviadas, quero %d", len(q.enviadas), n)
	}

	for i, err := range q.errosDeCtx {
		if err != nil {
			t.Fatalf("marcação %d recebeu um contexto JÁ CANCELADO (%v). "+
				"No banco de verdade essa escrita falharia, e a notificação — que FOI enviada — "+
				"voltaria pela fila e seria enviada de novo. É para isso que serve o WithoutCancel.",
				i, err)
		}
	}
}

// Falha no envio devolve a linha para a fila (MarkFailed decide entre 'pending'
// e 'failed' pelo número de tentativas). Nada é marcado como enviado.
func TestFalhaNoEnvioNaoMarcaComoEnviada(t *testing.T) {
	const n = 4

	q := &fakeQueue{pendentes: pendentes(n), segundoClaim: make(chan struct{})}
	s := &fakeSender{erro: errors.New("provedor de SMS fora do ar")}

	rodar(t, q, s, n)

	if len(q.falhadas) != n {
		t.Errorf("%d falhas registradas, quero %d", len(q.falhadas), n)
	}
	if len(q.enviadas) != 0 {
		t.Errorf("%d marcadas como enviadas, quero 0 — o envio falhou", len(q.enviadas))
	}
	if q.maxVisto != DefaultConfig().MaxAttempts {
		t.Errorf("MaxAttempts repassado = %d, quero %d", q.maxVisto, DefaultConfig().MaxAttempts)
	}

	// Mesmo no caminho de falha, a marcação não pode usar um contexto cancelado.
	for i, err := range q.errosDeCtx {
		if err != nil {
			t.Fatalf("marcação de falha %d recebeu contexto cancelado (%v)", i, err)
		}
	}
}
