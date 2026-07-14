package notification

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// queue é declarada aqui, pelo consumidor. O *PostgresRepo a satisfaz.
type queue interface {
	Claim(ctx context.Context, lote int, visibilidade time.Duration) ([]Notification, error)
	MarkSent(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, maxTentativas int, causa error) error
}

type Config struct {
	Workers      int           // goroutines de envio
	Batch        int           // quantas linhas reivindicar por vez
	PollInterval time.Duration // espera entre consultas quando a fila está vazia
	SendTimeout  time.Duration // teto de UM envio
	WriteTimeout time.Duration // teto para gravar o resultado no banco
	Visibility   time.Duration // devolve para a fila o que foi reivindicado e nunca concluído
	MaxAttempts  int
}

func DefaultConfig() Config {
	return Config{
		Workers:      4,
		Batch:        20,
		PollInterval: 2 * time.Second,
		SendTimeout:  10 * time.Second,
		WriteTimeout: 5 * time.Second,
		Visibility:   time.Minute,
		MaxAttempts:  5,
	}
}

type Dispatcher struct {
	queue  queue
	sender Sender
	cfg    Config

	fila chan Notification
	wg   sync.WaitGroup
}

func NewDispatcher(q queue, s Sender, cfg Config) *Dispatcher {
	return &Dispatcher{queue: q, sender: s, cfg: cfg}
}

// Run bloqueia até o ctx ser cancelado, e só retorna depois de DRENAR tudo que
// já estava em voo. Quem chama pode confiar que, quando Run volta, nenhuma
// goroutine deste dispatcher sobrou viva.
func (d *Dispatcher) Run(ctx context.Context) {
	// Buffer do tamanho do lote: o poller consegue despejar um lote inteiro sem
	// bloquear, mas não consegue reivindicar um segundo lote enquanto o primeiro
	// não for consumido. É o backpressure — a fila em memória nunca cresce sem
	// limite, e o banco não é consultado mais rápido do que os workers enviam.
	d.fila = make(chan Notification, d.cfg.Batch)

	d.wg.Add(d.cfg.Workers)
	for i := range d.cfg.Workers {
		go d.trabalhar(ctx, i)
	}
	slog.Info("dispatcher de notificações no ar", "workers", d.cfg.Workers)

	d.pollar(ctx) // roda até o ctx ser cancelado

	// Fechar o channel é o sinal de encerramento. Não existe "avisar cada
	// worker": o `range` deles termina sozinho quando o channel fecha E esvazia.
	// Um channel fechado que ainda tem itens continua entregando — é isso que
	// garante que nada em voo seja descartado.
	close(d.fila)
	d.wg.Wait()

	slog.Info("dispatcher de notificações encerrado")
}

func (d *Dispatcher) pollar(ctx context.Context) {
	tick := time.NewTicker(d.cfg.PollInterval)
	defer tick.Stop()

	for {
		lote, err := d.queue.Claim(ctx, d.cfg.Batch, d.cfg.Visibility)
		switch {
		case ctx.Err() != nil:
			return // shutdown, não erro
		case err != nil:
			slog.Error("reivindicando notificações", "erro", err)
			lote = nil // cai na espera em vez de martelar o banco em loop
		}

		for _, n := range lote {
			select {
			case d.fila <- n:
			case <-ctx.Done():
				// As notificações já reivindicadas e não despachadas ficam em
				// 'sending' no banco; o visibility timeout as devolve à fila.
				// Nada se perde.
				return
			}
		}

		// Lote cheio significa que provavelmente há mais esperando: volta já,
		// sem esperar o tick. É o que faz um acúmulo drenar rápido em vez de
		// escoar a `Batch` linhas a cada `PollInterval`.
		if len(lote) == d.cfg.Batch {
			continue
		}

		select {
		case <-tick.C:
		case <-ctx.Done():
			return
		}
	}
}

func (d *Dispatcher) trabalhar(ctx context.Context, id int) {
	defer d.wg.Done()

	// `range` sobre channel termina sozinho quando o channel é fechado e esvazia.
	// É a razão de este pool não precisar de um channel de "done", de um
	// contador, nem de sinalizar worker por worker.
	for n := range d.fila {
		d.despachar(ctx, n)
	}

	slog.Debug("worker de notificação encerrado", "worker", id)
}

func (d *Dispatcher) despachar(ctx context.Context, n Notification) {
	// A linha mais importante deste arquivo.
	//
	// No shutdown, o ctx do Run JÁ está cancelado — mas os workers ainda estão
	// drenando o que sobrou no channel. Se eles usassem esse ctx cancelado para
	// falar com o banco, o MarkSent falharia SEMPRE, e toda notificação enviada
	// durante o encerramento seria REENVIADA no próximo boot (o visibility
	// timeout a devolveria à fila).
	//
	// WithoutCancel herda os valores do ctx (trace, logger) e descarta o
	// cancelamento. O que limita o trabalho aqui passa a ser o timeout de cada
	// operação, não o sinal de shutdown.
	base := context.WithoutCancel(ctx)

	envio, cancelEnvio := context.WithTimeout(base, d.cfg.SendTimeout)
	defer cancelEnvio()

	erroEnvio := d.sender.Send(envio, n)

	// Timeout próprio para gravar o resultado: um banco travado não pode segurar
	// o encerramento do processo para sempre.
	registro, cancelRegistro := context.WithTimeout(base, d.cfg.WriteTimeout)
	defer cancelRegistro()

	if erroEnvio != nil {
		slog.Error("falha ao enviar notificação",
			"id", n.ID, "tipo", n.Kind, "tentativa", n.Attempts, "erro", erroEnvio)

		if err := d.queue.MarkFailed(registro, n.ID, d.cfg.MaxAttempts, erroEnvio); err != nil {
			// Não conseguimos nem registrar a falha. A linha fica em 'sending'
			// e o visibility timeout a devolve — o sistema se conserta sozinho.
			slog.Error("falha ao marcar notificação como falha", "id", n.ID, "erro", err)
		}
		return
	}

	if err := d.queue.MarkSent(registro, n.ID); err != nil {
		// Enviada com sucesso, mas não conseguimos gravar isso. O visibility
		// timeout vai devolvê-la à fila e ela SERÁ ENVIADA DE NOVO.
		//
		// Isto é at-least-once, e não exactly-once. Não é bug: exactly-once
		// exigiria commit atômico entre o Postgres e o provedor de SMS (2PC),
		// que não existe. Todo sistema de fila honesto entrega at-least-once e
		// empurra a idempotência para o consumidor final.
		slog.Error("notificação enviada mas NÃO marcada — será reenviada",
			"id", n.ID, "erro", err)
	}
}
