// Package main is the Formbricks Hub API server entrypoint.
package main

import (
	"context"
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
	"github.com/formbricks/hub/internal/workers"
	"github.com/formbricks/hub/pkg/database"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		return 1
	}

	// Configure slog with the log level from config
	setupLogging(cfg.LogLevel)

	// Initialize database connection
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		return 1
	}
	defer db.Close()

	// Initialize message publisher manager
	messageManager := service.NewMessagePublisherManager()

	// Webhooks: repository, River client, worker, provider, and CRUD service
	webhooksRepo := repository.NewWebhooksRepository(db)
	webhookSender := service.NewWebhookSenderImpl(webhooksRepo)
	webhookWorker := workers.NewWebhookDispatchWorker(webhooksRepo, webhookSender)
	riverWorkers := river.NewWorkers()
	river.AddWorker(riverWorkers, webhookWorker)

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: cfg.WebhookDeliveryMaxConcurrent},
		},
		Workers: riverWorkers,
	})
	if err != nil {
		slog.Error("Failed to create River client", "error", err)
		return 1
	}

	webhookProvider := service.NewWebhookProvider(riverClient, webhooksRepo, cfg.WebhookDeliveryMaxAttempts)
	messageManager.RegisterProvider(webhookProvider)

	// Start River in background
	go func() {
		if err := riverClient.Start(ctx); err != nil && err != context.Canceled {
			slog.Error("River client stopped with error", "error", err)
		}
	}()

	webhooksService := service.NewWebhooksService(webhooksRepo, messageManager)
	webhooksHandler := handlers.NewWebhooksHandler(webhooksService)

	// Initialize repository, service, and handler layers
	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	feedbackRecordsService := service.NewFeedbackRecordsService(feedbackRecordsRepo, messageManager)
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)
	healthHandler := handlers.NewHealthHandler()

	// Set up public endpoints (no authentication required)
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("GET /health", healthHandler.Check)

	// Apply middleware to public endpoints
	var publicHandler http.Handler = publicMux

	// Set up protected endpoints (authentication required)
	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("POST /v1/feedback-records", feedbackRecordsHandler.Create)
	protectedMux.HandleFunc("GET /v1/feedback-records", feedbackRecordsHandler.List)
	protectedMux.HandleFunc("GET /v1/feedback-records/{id}", feedbackRecordsHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/feedback-records/{id}", feedbackRecordsHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/feedback-records/{id}", feedbackRecordsHandler.Delete)
	protectedMux.HandleFunc("DELETE /v1/feedback-records", feedbackRecordsHandler.BulkDelete)
	protectedMux.HandleFunc("POST /v1/webhooks", webhooksHandler.Create)
	protectedMux.HandleFunc("GET /v1/webhooks", webhooksHandler.List)
	protectedMux.HandleFunc("GET /v1/webhooks/{id}", webhooksHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/webhooks/{id}", webhooksHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/webhooks/{id}", webhooksHandler.Delete)

	// Apply middleware to protected endpoints
	var protectedHandler http.Handler = protectedMux
	protectedHandler = middleware.Auth(cfg.APIKey)(protectedHandler)

	// Combine both handlers
	mainMux := http.NewServeMux()
	mainMux.Handle("/v1/", protectedHandler)
	mainMux.Handle("/", publicHandler) // Catch-all for public routes (/health, etc.)

	// Apply logging to all requests
	handler := middleware.Logging(mainMux)

	// Create HTTP server
	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		slog.Info("Starting server", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server error", "error", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
		messageManager.Shutdown()
		if stopErr := riverClient.Stop(ctx); stopErr != nil {
			slog.Error("River client stop after server shutdown error", "error", stopErr)
		}
		return 1
	}

	messageManager.Shutdown()
	if err := riverClient.Stop(ctx); err != nil {
		slog.Warn("River client stop", "error", err)
	}

	slog.Info("Server exited")
	return 0
}

// setupLogging configures slog with the specified log level
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
