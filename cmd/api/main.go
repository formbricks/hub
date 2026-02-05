// Package main is the Formbricks Hub API server entrypoint.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/api/middleware"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
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
	// Set up logging early so config load errors use the same handler (default: info).
	setupLogging(getLogLevelEnv())

	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		return exitFailure
	}
	setupLogging(cfg.LogLevel)

	// Initialize database connection
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		return exitFailure
	}
	defer db.Close()

	// Initialize repository, service, and handler layers
	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	feedbackRecordsService := service.NewFeedbackRecordsService(feedbackRecordsRepo)
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)
	healthHandler := handlers.NewHealthHandler()

	// Public endpoints (no authentication)
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("GET /health", healthHandler.Check)

	// Protected endpoints (API key required)
	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("POST /v1/feedback-records", feedbackRecordsHandler.Create)
	protectedMux.HandleFunc("GET /v1/feedback-records", feedbackRecordsHandler.List)
	protectedMux.HandleFunc("GET /v1/feedback-records/{id}", feedbackRecordsHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/feedback-records/{id}", feedbackRecordsHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/feedback-records/{id}", feedbackRecordsHandler.Delete)
	protectedMux.HandleFunc("DELETE /v1/feedback-records", feedbackRecordsHandler.BulkDelete)
	protectedHandler := middleware.Auth(cfg.APIKey)(protectedMux)

	// Mount protected under /v1/, public (e.g. /health) at /
	mainMux := http.NewServeMux()
	mainMux.Handle("/v1/", protectedHandler)
	mainMux.Handle("/", publicMux)

	handler := middleware.Logging(mainMux)

	// Create HTTP server
	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine; report start failure so we can exit non-zero
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("Starting server", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Wait for interrupt signal or server start failure
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-serverErr:
		slog.Error("Server failed to start", "error", err)
		return exitFailure
	case sig := <-quit:
		slog.Info("Received signal, shutting down", "signal", sig)
		signal.Reset(syscall.SIGINT, syscall.SIGTERM)
	}

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("Server shutdown failed", "error", err)
		return exitFailure
	}

	slog.Info("Server stopped")
	return exitSuccess
}

// getLogLevelEnv returns LOG_LEVEL from the environment for use before config is loaded.
func getLogLevelEnv() string {
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		return v
	}
	return "info"
}

// setupLogging configures slog with the specified log level.
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

	opts := &slog.HandlerOptions{
		Level: logLevel,
	}
	handler := slog.NewTextHandler(os.Stdout, opts)
	slog.SetDefault(slog.New(handler))
}
