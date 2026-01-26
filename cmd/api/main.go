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
	"github.com/formbricks/hub/internal/embeddings"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
)

func main() {
	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Configure slog with the log level from config
	setupLogging(cfg.LogLevel)

	// Initialize database connection
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Initialize embedding client if OpenAI API key is configured
	var embeddingClient embeddings.Client
	if cfg.OpenAIAPIKey != "" {
		embeddingClient = embeddings.NewOpenAIClient(cfg.OpenAIAPIKey)
		slog.Info("AI enrichment enabled", "embedding_model", "text-embedding-3-small")
	} else {
		slog.Info("AI enrichment disabled (OPENAI_API_KEY not set)")
	}

	// Initialize repository, service, and handler layers
	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	var feedbackRecordsService *service.FeedbackRecordsService
	if embeddingClient != nil {
		feedbackRecordsService = service.NewFeedbackRecordsServiceWithEmbeddings(feedbackRecordsRepo, embeddingClient)
	} else {
		feedbackRecordsService = service.NewFeedbackRecordsService(feedbackRecordsRepo)
	}
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)

	knowledgeRecordsRepo := repository.NewKnowledgeRecordsRepository(db)
	var knowledgeRecordsService *service.KnowledgeRecordsService
	if embeddingClient != nil {
		knowledgeRecordsService = service.NewKnowledgeRecordsServiceWithEmbeddings(knowledgeRecordsRepo, embeddingClient)
	} else {
		knowledgeRecordsService = service.NewKnowledgeRecordsService(knowledgeRecordsRepo)
	}
	knowledgeRecordsHandler := handlers.NewKnowledgeRecordsHandler(knowledgeRecordsService)

	topicsRepo := repository.NewTopicsRepository(db)
	var topicsService *service.TopicsService
	if embeddingClient != nil {
		topicsService = service.NewTopicsServiceWithEmbeddings(topicsRepo, embeddingClient)
	} else {
		topicsService = service.NewTopicsService(topicsRepo)
	}
	topicsHandler := handlers.NewTopicsHandler(topicsService)

	healthHandler := handlers.NewHealthHandler()

	// Set up public endpoints (no authentication required)
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("GET /health", healthHandler.Check)

	// Apply middleware to public endpoints
	var publicHandler http.Handler = publicMux
	// publicHandler = middleware.CORS(publicHandler) // CORS disabled

	// Set up protected endpoints (authentication required)
	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("POST /v1/feedback-records", feedbackRecordsHandler.Create)
	protectedMux.HandleFunc("GET /v1/feedback-records", feedbackRecordsHandler.List)
	protectedMux.HandleFunc("GET /v1/feedback-records/{id}", feedbackRecordsHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/feedback-records/{id}", feedbackRecordsHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/feedback-records/{id}", feedbackRecordsHandler.Delete)
	protectedMux.HandleFunc("DELETE /v1/feedback-records", feedbackRecordsHandler.BulkDelete)

	protectedMux.HandleFunc("POST /v1/knowledge-records", knowledgeRecordsHandler.Create)
	protectedMux.HandleFunc("GET /v1/knowledge-records", knowledgeRecordsHandler.List)
	protectedMux.HandleFunc("GET /v1/knowledge-records/{id}", knowledgeRecordsHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/knowledge-records/{id}", knowledgeRecordsHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/knowledge-records/{id}", knowledgeRecordsHandler.Delete)
	protectedMux.HandleFunc("DELETE /v1/knowledge-records", knowledgeRecordsHandler.BulkDelete)

	protectedMux.HandleFunc("POST /v1/topics", topicsHandler.Create)
	protectedMux.HandleFunc("GET /v1/topics", topicsHandler.List)
	protectedMux.HandleFunc("GET /v1/topics/{id}", topicsHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/topics/{id}", topicsHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/topics/{id}", topicsHandler.Delete)

	// Apply middleware to protected endpoints
	var protectedHandler http.Handler = protectedMux
	protectedHandler = middleware.Auth(cfg.APIKey)(protectedHandler)
	// protectedHandler = middleware.CORS(protectedHandler)	// CORS disabled

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
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}

	slog.Info("Server exited")
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
