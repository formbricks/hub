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

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/api/middleware"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
	"github.com/formbricks/hub/pkg/database"
)

const (
	exitSuccess = 0
	exitFailure = 1

	metricsReadHeaderTimeout = 10 * time.Second
	auxShutdownTimeout       = 5 * time.Second // metrics server and MeterProvider
	serverReadTimeout        = 15 * time.Second
	serverWriteTimeout       = 15 * time.Second
	serverIdleTimeout        = 60 * time.Second
)

func main() {
	os.Exit(run())
}

func run() int {
	// Set up logging early so config load errors use the same handler (default: info).
	setupLogging(getLogLevelEnv())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	// Metrics (optional): when PrometheusEnabled, create MeterProvider and custom metrics; otherwise no-op
	var (
		metrics       observability.HubMetrics
		meterProvider observability.MeterProviderShutdown
		metricsServer *http.Server
	)

	if cfg.PrometheusEnabled {
		mp, metricsHandler, m, err := observability.NewMeterProvider(ctx, observability.MeterProviderConfig{})
		if err != nil {
			slog.Error("Failed to create MeterProvider", "error", err)

			return exitFailure
		}

		meterProvider = mp
		metrics = m
		metricsServer = &http.Server{
			Addr:              ":" + cfg.PrometheusExporterPort,
			Handler:           metricsHandler,
			ReadHeaderTimeout: metricsReadHeaderTimeout,
		}

		go func() {
			slog.Info("Starting metrics server", "port", cfg.PrometheusExporterPort)

			if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("Metrics server failed", "error", err)
			}
		}()
	}

	defer func() {
		if metricsServer != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), auxShutdownTimeout)
			if err := metricsServer.Shutdown(shutdownCtx); err != nil {
				slog.Warn("Metrics server shutdown", "error", err)
			}

			cancel()
		}

		if meterProvider != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), auxShutdownTimeout)
			if err := meterProvider.Shutdown(shutdownCtx); err != nil {
				slog.Warn("MeterProvider shutdown", "error", err)
			}

			cancel()
		}
	}()

	// Initialize message publisher manager
	messageManager := service.NewMessagePublisherManager(cfg.MessagePublisherBufferSize, cfg.MessagePublisherPerEventTimeout, metrics)

	// Webhooks: repository, River client, worker, provider, and CRUD service
	webhooksRepo := repository.NewWebhooksRepository(db)
	webhookSender := service.NewWebhookSenderImpl(webhooksRepo, metrics)
	webhookWorker := workers.NewWebhookDispatchWorker(webhooksRepo, webhookSender, metrics)
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
		messageManager.Shutdown()

		return exitFailure
	}

	webhookProvider := service.NewWebhookProvider(riverClient, webhooksRepo, cfg.WebhookDeliveryMaxAttempts, cfg.WebhookMaxFanOutPerEvent, metrics)
	messageManager.RegisterProvider(webhookProvider)

	// Start River in background
	go func() {
		if err := riverClient.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
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
	protectedMux.HandleFunc("POST /v1/webhooks", webhooksHandler.Create)
	protectedMux.HandleFunc("GET /v1/webhooks", webhooksHandler.List)
	protectedMux.HandleFunc("GET /v1/webhooks/{id}", webhooksHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/webhooks/{id}", webhooksHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/webhooks/{id}", webhooksHandler.Delete)
	protectedHandler := middleware.Auth(cfg.APIKey)(protectedMux)

	// Mount protected under /v1/, public (e.g. /health) at /
	mainMux := http.NewServeMux()
	mainMux.Handle("/v1/", protectedHandler)
	mainMux.Handle("/", publicMux)

	// Metrics outermost so duration is full request time
	handler := middleware.Metrics(metrics)(middleware.Logging(mainMux))

	// Create HTTP server
	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
		IdleTimeout:  serverIdleTimeout,
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
		messageManager.Shutdown()

		stopCtx, stopCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		if stopErr := riverClient.Stop(stopCtx); stopErr != nil {
			slog.Warn("River client stop after server start failure", "error", stopErr)
		}

		stopCancel()

		return exitFailure
	case sig := <-quit:
		slog.Info("Received signal, shutting down", "signal", sig)
		signal.Reset(syscall.SIGINT, syscall.SIGTERM)
	}

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("Server shutdown failed", "error", err)
		messageManager.Shutdown()

		if stopErr := riverClient.Stop(shutdownCtx); stopErr != nil {
			slog.Error("River client stop after server shutdown error", "error", stopErr)
		}

		return exitFailure
	}

	messageManager.Shutdown()

	if err := riverClient.Stop(shutdownCtx); err != nil {
		slog.Warn("River client stop", "error", err)
	}
	// Metrics server and MeterProvider are shut down via defer

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
