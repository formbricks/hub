// Package main is the Formbricks Hub worker entrypoint (hub-worker).
// It runs River job workers (webhook delivery, embeddings) and does not expose HTTP.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	pgxvec "github.com/pgvector/pgvector-go/pgx"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/pkg/database"
)

const (
	exitSuccess = 0
	exitFailure = 1
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)

		return exitFailure
	}

	if cfg.Database.URL == "" || !config.DatabaseURLConfigured() {
		slog.Error("DATABASE_URL must be set explicitly for hub-worker (do not use the default test URL)")

		return exitFailure
	}

	ctx := context.Background()

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
		database.WithAfterConnect(pgxvec.RegisterTypes),
	)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)

		return exitFailure
	}
	defer db.Close()

	app, err := NewWorkerApp(cfg, db)
	if err != nil {
		slog.Error("Failed to create worker app", "error", err)

		return exitFailure
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.Run(sigCtx); err != nil {
		slog.Error("Worker failed", "error", err)

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.Duration())
		defer cancel()

		if shutdownErr := app.Shutdown(shutdownCtx); shutdownErr != nil {
			slog.Warn("Shutdown error", "error", shutdownErr)
		}

		return exitFailure
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.Duration())
	defer cancel()

	if err := app.Shutdown(shutdownCtx); err != nil {
		slog.Error("Shutdown failed", "error", err)

		return exitFailure
	}

	slog.Info("Worker stopped")

	return exitSuccess
}
