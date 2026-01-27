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
	connectorservice "github.com/formbricks/hub/internal/connector/service"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
	"github.com/formbricks/hub/pkg/hub"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Configure slog
	setupLogging(cfg.LogLevel)

	// Initialize database connection
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	slog.Info("Connector service starting",
		"max_polling_instances", cfg.MaxPollingConnectorInstances,
		"max_webhook_instances", cfg.MaxWebhookConnectorInstances,
		"hub_base_url", os.Getenv("HUB_BASE_URL"),
	)

	// Initialize repository
	connectorInstanceRepo := repository.NewConnectorInstanceRepository(db)

	// Initialize Hub API client
	hubBaseURL := os.Getenv("HUB_BASE_URL")
	if hubBaseURL == "" {
		hubBaseURL = "http://localhost:8080" // Default
	}
	hubAPIKey := os.Getenv("HUB_API_KEY")
	if hubAPIKey == "" {
		hubAPIKey = cfg.APIKey // Fallback to same API key
	}
	hubClient := hub.NewClient(hubBaseURL, hubAPIKey)

	// Initialize connector service components
	registry := connectorservice.NewRegistry()
	rateLimiter := connectorservice.NewRateLimiter(
		cfg.PollingConnectorMinDelay,
		cfg.PollingConnectorMaxPollsPerHour,
	)

	// Register connector factories
	registry.Register(&connectorservice.FormbricksFactory{})
	registry.Register(&connectorservice.TypeformFactory{})

	// Initialize PollerManager
	pollerManager := connectorservice.NewPollerManager(
		connectorInstanceRepo,
		registry,
		rateLimiter,
		hubClient,
		cfg,
	)

	// Initialize connector instance service and handler for API
	connectorInstanceService := service.NewConnectorInstanceService(connectorInstanceRepo, cfg)
	connectorInstanceHandler := handlers.NewConnectorInstanceHandler(connectorInstanceService)
	healthHandler := handlers.NewHealthHandler()

	// Set up HTTP server for connector instance management API
	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("POST /v1/connector-instances", connectorInstanceHandler.Create)
	protectedMux.HandleFunc("GET /v1/connector-instances", connectorInstanceHandler.List)
	protectedMux.HandleFunc("GET /v1/connector-instances/{id}", connectorInstanceHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/connector-instances/{id}", connectorInstanceHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/connector-instances/{id}", connectorInstanceHandler.Delete)
	protectedMux.HandleFunc("POST /v1/connector-instances/{id}/start", connectorInstanceHandler.Start)
	protectedMux.HandleFunc("POST /v1/connector-instances/{id}/stop", connectorInstanceHandler.Stop)

	var protectedHandler http.Handler = protectedMux
	protectedHandler = middleware.Auth(cfg.APIKey)(protectedHandler)

	// Set up public endpoints
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("GET /health", healthHandler.Check)

	// Combine handlers
	mainMux := http.NewServeMux()
	mainMux.Handle("/v1/", protectedHandler)
	mainMux.Handle("/", publicMux)

	// Apply logging middleware
	handler := middleware.Logging(mainMux)

	// Determine port for connector service
	port := os.Getenv("CONNECTOR_SERVICE_PORT")
	if port == "" {
		port = "8081" // Default port for connector service
	}

	// Create HTTP server
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start HTTP server in a goroutine
	go func() {
		slog.Info("Starting connector service HTTP server", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	// Start PollerManager
	if err := pollerManager.Start(ctx); err != nil {
		slog.Error("Failed to start poller manager", "error", err)
		os.Exit(1)
	}

	// Wait for interrupt signal to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down connector service...")
	cancel() // Cancel context to stop poller manager

	// Stop poller manager
	pollerManager.Stop()

	// Shutdown HTTP server
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}

	slog.Info("Connector service exited")
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
