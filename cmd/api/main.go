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
	"github.com/formbricks/hub/internal/jobs"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/worker"
	"github.com/formbricks/hub/pkg/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"golang.org/x/time/rate"
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

	// Initialize repositories
	topicsRepo := repository.NewTopicsRepository(db)
	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	knowledgeRecordsRepo := repository.NewKnowledgeRecordsRepository(db)

	// Initialize River job queue if enabled and embedding client is configured
	var riverClient *river.Client[pgx.Tx]
	var jobInserter jobs.JobInserter
	if cfg.RiverEnabled && embeddingClient != nil {
		var err error
		riverClient, err = initRiver(ctx, db, cfg, embeddingClient, feedbackRecordsRepo, topicsRepo, knowledgeRecordsRepo)
		if err != nil {
			slog.Error("Failed to initialize River job queue", "error", err)
			os.Exit(1)
		}
		jobInserter = jobs.NewRiverJobInserter(riverClient)
		slog.Info("River job queue enabled",
			"workers", cfg.RiverWorkers,
			"max_retries", cfg.RiverMaxRetries,
			"rate_limit", cfg.EmbeddingRateLimit,
		)
	} else if cfg.OpenAIAPIKey != "" && !cfg.RiverEnabled {
		slog.Info("River job queue disabled (RIVER_ENABLED=false), using legacy goroutines")
	}

	// Initialize services with optional job inserter
	var topicsService *service.TopicsService
	if embeddingClient != nil {
		topicsService = service.NewTopicsServiceWithEmbeddings(topicsRepo, embeddingClient, jobInserter)
	} else {
		topicsService = service.NewTopicsService(topicsRepo)
	}
	topicsHandler := handlers.NewTopicsHandler(topicsService)

	var feedbackRecordsService *service.FeedbackRecordsService
	if embeddingClient != nil {
		feedbackRecordsService = service.NewFeedbackRecordsServiceWithEmbeddings(feedbackRecordsRepo, embeddingClient, jobInserter)
	} else {
		feedbackRecordsService = service.NewFeedbackRecordsService(feedbackRecordsRepo)
	}
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)

	var knowledgeRecordsService *service.KnowledgeRecordsService
	if embeddingClient != nil {
		knowledgeRecordsService = service.NewKnowledgeRecordsServiceWithEmbeddings(knowledgeRecordsRepo, embeddingClient, jobInserter)
	} else {
		knowledgeRecordsService = service.NewKnowledgeRecordsService(knowledgeRecordsRepo)
	}
	knowledgeRecordsHandler := handlers.NewKnowledgeRecordsHandler(knowledgeRecordsService)

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
	protectedMux.HandleFunc("GET /v1/topics/{id}/children", topicsHandler.GetChildren)
	protectedMux.HandleFunc("GET /v1/topics/{id}", topicsHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/topics/{id}", topicsHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/topics/{id}", topicsHandler.Delete)

	// Taxonomy generation endpoints (calls Python microservice)
	taxonomyClient := service.NewTaxonomyClient(cfg.TaxonomyServiceURL)
	clusteringJobsRepo := repository.NewClusteringJobsRepository(db)
	taxonomyHandler := handlers.NewTaxonomyHandlerWithSchedule(taxonomyClient, clusteringJobsRepo)
	protectedMux.HandleFunc("POST /v1/taxonomy/{tenant_id}/generate", taxonomyHandler.Generate)
	protectedMux.HandleFunc("POST /v1/taxonomy/{tenant_id}/generate/sync", taxonomyHandler.GenerateSync)
	protectedMux.HandleFunc("GET /v1/taxonomy/{tenant_id}/status", taxonomyHandler.Status)
	protectedMux.HandleFunc("GET /v1/taxonomy/health", taxonomyHandler.Health)
	// Schedule management
	protectedMux.HandleFunc("POST /v1/taxonomy/{tenant_id}/schedule", taxonomyHandler.CreateSchedule)
	protectedMux.HandleFunc("GET /v1/taxonomy/{tenant_id}/schedule", taxonomyHandler.GetSchedule)
	protectedMux.HandleFunc("DELETE /v1/taxonomy/{tenant_id}/schedule", taxonomyHandler.DeleteSchedule)
	protectedMux.HandleFunc("GET /v1/taxonomy/schedules", taxonomyHandler.ListSchedules)

	// Apply middleware to protected endpoints
	// Order matters: CORS must wrap Auth so OPTIONS preflight requests bypass authentication
	var protectedHandler http.Handler = protectedMux
	protectedHandler = middleware.Auth(cfg.APIKey)(protectedHandler)
	protectedHandler = middleware.CORS(protectedHandler) // CORS wraps Auth

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

	// Start taxonomy scheduler if enabled
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	if cfg.TaxonomySchedulerEnabled {
		taxonomyScheduler := worker.NewTaxonomyScheduler(
			clusteringJobsRepo,
			taxonomyClient,
			cfg.TaxonomyPollInterval,
			5, // batch size
		)
		go taxonomyScheduler.Start(workerCtx)
	}

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Stop accepting new HTTP requests
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	// 2. Stop River (waits for in-flight jobs to complete)
	if riverClient != nil {
		slog.Info("Stopping River job queue...")
		if err := riverClient.Stop(shutdownCtx); err != nil {
			slog.Error("River forced to shutdown", "error", err)
		}
		slog.Info("River job queue stopped")
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

// initRiver initializes the River job queue client and workers
func initRiver(
	ctx context.Context,
	db *pgxpool.Pool,
	cfg *config.Config,
	embeddingClient embeddings.Client,
	feedbackRepo *repository.FeedbackRecordsRepository,
	topicsRepo *repository.TopicsRepository,
	knowledgeRepo *repository.KnowledgeRecordsRepository,
) (*river.Client[pgx.Tx], error) {
	// Create rate limiter for OpenAI API calls
	rateLimiter := rate.NewLimiter(rate.Limit(cfg.EmbeddingRateLimit), 1)

	// Create embedding worker with dependencies
	embeddingWorker := jobs.NewEmbeddingWorker(jobs.EmbeddingWorkerDeps{
		EmbeddingClient:  embeddingClient,
		FeedbackUpdater:  jobs.NewFeedbackRecordsUpdater(feedbackRepo),
		TopicUpdater:     jobs.NewTopicsUpdater(topicsRepo),
		KnowledgeUpdater: jobs.NewKnowledgeRecordsUpdater(knowledgeRepo),
		RateLimiter:      rateLimiter,
	})

	// Register workers
	workers := river.NewWorkers()
	river.AddWorker(workers, embeddingWorker)

	// Create River client
	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: cfg.RiverWorkers},
		},
		Workers:      workers,
		ErrorHandler: &jobs.ErrorHandler{},
		JobTimeout:   60 * time.Second, // Timeout for individual jobs
		MaxAttempts:  cfg.RiverMaxRetries,
	})
	if err != nil {
		return nil, err
	}

	// Start River (begins processing jobs)
	if err := riverClient.Start(ctx); err != nil {
		return nil, err
	}

	return riverClient, nil
}
