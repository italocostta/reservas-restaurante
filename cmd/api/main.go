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
	"reservas-restaurante/internal/reservation"
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
	tableHandler := table.NewHandler(tableRepo)

	// reservationRepo entra DUAS VEZES no allocator — como TableFinder e como
	// ReservationCreator. É a mesma struct, fatiada por duas interfaces pequenas
	// que o allocator declarou. Ele não sabe que é o mesmo objeto, e não precisa.
	reservationRepo := reservation.NewPostgresRepo(pool, cfg.ServiceTZ)
	allocator := reservation.NewAllocator(
		reservationRepo,
		reservationRepo,
		// Aqui é onde a Config (infra) vira ServiceHours (domínio). O pacote
		// reservation não importa config: quem traduz é o main.go.
		reservation.ServiceHours{
			Start: cfg.ServiceStart,
			End:   cfg.ServiceEnd,
			TZ:    cfg.ServiceTZ,
		},
		reservation.SystemClock{},
	)
	reservationHandler := reservation.NewHandler(allocator, reservationRepo)

	router := httpserver.New(cfg, tableHandler, reservationHandler)

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

	// context.Background() e não ctx: o ctx JÁ foi cancelado pelo sinal — usá-lo
	// aqui abortaria o shutdown no mesmo instante, que é o oposto de gracioso.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
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
