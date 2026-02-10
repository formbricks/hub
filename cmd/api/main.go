// Package main is the Formbricks Hub API server entrypoint.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

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
	setupLogging(getLogLevelEnv())

	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)

		return exitFailure
	}

	setupLogging(cfg.LogLevel)

	ctx := context.Background()

	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
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

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		if shutdownErr := app.Shutdown(shutdownCtx); shutdownErr != nil {
			slog.Warn("Shutdown error", "error", shutdownErr)
		}

		return exitFailure
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := app.Shutdown(shutdownCtx); err != nil {
		slog.Error("Shutdown failed", "error", err)

		return exitFailure
	}

	slog.Info("Server stopped")

	return exitSuccess
}

func getLogLevelEnv() string {
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		return v
	}

	return "info"
}

func setupLogging(level string) {
	var logLevel slog.Level

	switch strings.ToLower(level) {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: logLevel}
	handler := slog.NewTextHandler(os.Stdout, opts)
	slog.SetDefault(slog.New(handler))
}
