package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"reservas-restaurante/internal/config"
	"reservas-restaurante/internal/httpserver"
	"reservas-restaurante/internal/notification"
	"reservas-restaurante/internal/reservation"
	"reservas-restaurante/internal/settings"
	"reservas-restaurante/internal/table"
)

//	@title			API de Reservas de Restaurante
//	@version		1.0
//	@description	Backend de reservas de mesas de um restaurante único. Staff cria reservas; o sistema aloca mesa automaticamente respeitando capacidade e evitando sobreposição de horário.
//	@host			localhost:8080
//	@BasePath		/
func main() {
	if err := run(); err != nil {
		slog.Error("aplicação encerrada com erro", "erro", err)
		os.Exit(1)
	}
}

// run existe porque os.Exit NÃO roda os defers. Se toda a lógica estivesse em
// main(), qualquer os.Exit(1) vazaria o pool de conexões. Aqui os defers
// rodam de verdade, e main() só traduz erro em código de saída.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.SetDefault(newLogger(cfg.Env))

	// Contexto cancelado no Ctrl+C / SIGTERM: é o gatilho do shutdown gracioso.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("abrindo pool: %w", err)
	}
	defer pool.Close()

	// pgxpool.New NÃO conecta — só valida a string e prepara o pool preguiçoso.
	// O Ping é o que prova que o host existe, que a senha está certa e que o
	// SSL negociou. Sem ele, o primeiro erro de conexão apareceria na primeira
	// requisição de um usuário, não no boot.
	pingCtx, cancelPing := context.WithTimeout(ctx, 10*time.Second)
	defer cancelPing()
	if err := pool.Ping(pingCtx); err != nil {
		return fmt.Errorf("conectando no Postgres: %w", err)
	}
	slog.Info("conectado no Postgres")

	// O "container de injeção de dependência" do projeto é este trecho. Sem
	// reflexão, sem scan de pacote, sem anotação: construtores chamados na ordem
	// em que as dependências existem.
	tableRepo := table.NewPostgresRepo(pool)

	// reservationRepo entra DUAS VEZES no allocator — como TableFinder e como
	// ReservationCreator. É a mesma struct, fatiada por duas interfaces pequenas
	// que o allocator declarou. Ele não sabe que é o mesmo objeto, e não precisa.
	reservationRepo := reservation.NewPostgresRepo(pool, cfg.ServiceTZ)

	// E aqui ela entra uma TERCEIRA vez, agora atravessando a fronteira no sentido
	// contrário: o table.Handler precisa saber se uma mesa tem reserva futura antes
	// de deixar alguém desativá-la.
	//
	// Repare no que NÃO aconteceu: o pacote `table` não importou `reservation`. Ele
	// declarou uma interface de um método (`agenda`) descrevendo a pergunta que
	// precisa fazer, e o *reservation.PostgresRepo a satisfaz sem nunca ter ouvido
	// falar dela. O acoplamento existe, é real, e mora AQUI — num único argumento de
	// construtor, onde dá para vê-lo. Em Spring isso seria um @Autowired num campo,
	// e a dependência entre os dois domínios ficaria invisível até alguém rodar o
	// grafo de beans.
	tableHandler := table.NewHandler(tableRepo, reservationRepo)

	// A config editável do restaurante (migration 0009). O settingsRepo satisfaz a
	// reservation.ExpedienteVigente — é por ele que o horário e os dias de
	// funcionamento gravados no banco chegam ao allocator e à agenda, dinamicamente.
	settingsRepo := settings.NewPostgresRepo(pool)
	settingsHandler := settings.NewHandler(settingsRepo)

	// O expediente NÃO vem mais da Config para o domínio: vem do settingsRepo, que
	// lê a config editável do banco (migration 0009). Editar o horário passou a
	// afetar as reservas novas — antes era um valor fixo do boot.
	//
	// reservationRepo entra três vezes: TableFinder + ReservationCreator +
	// ReservationReplacer. A mesma struct, interfaces pequenas declaradas pelo
	// allocator. Ele não sabe que é o mesmo objeto — nem precisa.
	allocator := reservation.NewAllocator(
		reservationRepo,
		reservationRepo,
		reservationRepo,
		settingsRepo,
		reservation.SystemClock{},
	)
	schedule := reservation.NewSchedule(reservationRepo, settingsRepo)
	reservationHandler := reservation.NewHandler(allocator, reservationRepo, schedule)

	router := httpserver.New(cfg, tableHandler, reservationHandler, settingsHandler)

	// O dispatcher tem contexto PRÓPRIO, derivado com WithoutCancel do ctx do
	// sinal. Ele precisa sobreviver ao Ctrl+C para ser derrubado só DEPOIS do
	// servidor HTTP — ver a ordem de encerramento lá embaixo.
	dispatcherCtx, pararDispatcher := context.WithCancel(context.WithoutCancel(ctx))
	defer pararDispatcher()

	dispatcher := notification.NewDispatcher(
		notification.NewPostgresRepo(pool),
		notification.LogSender{},
		notification.DefaultConfig(),
	)

	dispatcherParou := make(chan struct{})
	go func() {
		defer close(dispatcherParou)
		dispatcher.Run(dispatcherCtx)
	}()

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
		// http.ListenAndServe(addr, handler) não tem NENHUM timeout por padrão:
		// um cliente lento segura a conexão para sempre (Slowloris). Por isso
		// montamos o Server à mão.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Buffer 1: se ninguém ler o canal (caso do shutdown por sinal), a goroutine
	// não fica bloqueada para sempre escrevendo nele.
	errCh := make(chan error, 1)
	go func() {
		slog.Info("servidor ouvindo", "porta", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("servidor: %w", err)
	case <-ctx.Done():
		slog.Info("sinal recebido, encerrando")
	}

	// ORDEM DE ENCERRAMENTO — importa, e o motivo é sutil.
	//
	// 1º o servidor HTTP: para de aceitar requisições novas e drena as em voo.
	//    Uma reserva sendo criada neste instante ainda vai enfileirar sua
	//    notificação, na transação dela.
	// 2º o dispatcher: só então ele é derrubado, drenando o que já pegou.
	//
	// Se a ordem fosse invertida, as reservas criadas nos últimos segundos
	// ficariam com a notificação enfileirada e ninguém neste processo para
	// despachá-la. Repare que isso ATRASA a notificação, mas não a PERDE — a
	// linha continua 'pending' no banco e o próximo boot a pega. É o outbox
	// pagando de novo: até a ordem do shutdown virou questão de latência, e
	// não de corretude.

	// context.Background() e não ctx: o ctx JÁ foi cancelado pelo sinal — usá-lo
	// aqui abortaria o shutdown no mesmo instante, que é o oposto de gracioso.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown do servidor: %w", err)
	}

	pararDispatcher()

	select {
	case <-dispatcherParou:
	case <-time.After(15 * time.Second):
		// Não é perda: as notificações reivindicadas e não concluídas continuam
		// em 'sending', e o visibility timeout as devolve à fila no próximo boot.
		slog.Error("dispatcher não encerrou a tempo — o que estava em voo volta pela fila")
	}

	slog.Info("encerrado")
	return nil
}

func newLogger(env string) *slog.Logger {
	if env == "production" {
		return slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
