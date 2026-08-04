// Package main is the Formbricks Hub API server entrypoint.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	pgxvec "github.com/pgvector/pgvector-go/pgx"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/observability"
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
		setupLogging("info", "text")
		slog.Error("Failed to load configuration", "error", err)

		return exitFailure
	}

	if cfg.Server.HubAPIKey == "" {
		setupLogging(cfg.Server.LogLevel, cfg.Server.LogFormat)
		slog.Error("API_KEY is required for hub-api")

		return exitFailure
	}

	setupLogging(cfg.Server.LogLevel, cfg.Server.LogFormat)

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

	app, err := NewApp(cfg, db)
	if err != nil {
		slog.Error("Failed to create application", "error", err)

		return exitFailure
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.Run(sigCtx); err != nil {
		slog.Error("Component failed, exiting", "error", err)

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

	slog.Info("Server stopped")

	return exitSuccess
}

func setupLogging(level, format string) {
	slog.SetDefault(slog.New(observability.NewLogHandler(os.Stdout, level, format)))
}
