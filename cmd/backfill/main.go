// Package main provides a CLI tool to backfill embeddings for existing records.
// This enqueues River jobs for all records that are missing embeddings.
//
// Usage:
//
//	go run cmd/backfill/main.go
//
// Or after building:
//
//	./bin/backfill
//
// Environment variables:
//   - DATABASE_URL: PostgreSQL connection string (required)
//   - OPENAI_API_KEY: OpenAI API key (required for embedding generation)
//   - RIVER_WORKERS: Number of concurrent workers (default: 10)
//   - EMBEDDING_RATE_LIMIT: OpenAI requests per second (default: 50)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/embeddings"
	"github.com/formbricks/hub/internal/jobs"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/database"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"golang.org/x/time/rate"
)

func main() {
	ctx := context.Background()

	// Configure logging
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("Starting embedding backfill...")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	if cfg.OpenAIAPIKey == "" {
		slog.Error("OPENAI_API_KEY is required for embedding generation")
		os.Exit(1)
	}

	// Connect to database
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Initialize embedding client
	embeddingClient := embeddings.NewOpenAIClient(cfg.OpenAIAPIKey)

	// Initialize repositories for the worker
	feedbackRepo := repository.NewFeedbackRecordsRepository(db)
	topicsRepo := repository.NewTopicsRepository(db)
	knowledgeRepo := repository.NewKnowledgeRecordsRepository(db)

	// Create rate limiter
	rateLimiter := rate.NewLimiter(rate.Limit(cfg.EmbeddingRateLimit), 1)

	// Create embedding worker
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
		JobTimeout:   60 * time.Second,
		MaxAttempts:  cfg.RiverMaxRetries,
	})
	if err != nil {
		slog.Error("Failed to create River client", "error", err)
		os.Exit(1)
	}

	// Start River
	if err := riverClient.Start(ctx); err != nil {
		slog.Error("Failed to start River", "error", err)
		os.Exit(1)
	}

	// Create job inserter
	inserter := jobs.NewRiverJobInserter(riverClient)

	// Run backfill
	slog.Info("Enqueueing embedding jobs for records without embeddings...")
	stats, err := jobs.Backfill(ctx, db, inserter)
	if err != nil {
		slog.Error("Backfill failed", "error", err)
	}

	// Print results
	fmt.Println()
	fmt.Println("Backfill Summary")
	fmt.Println("================")
	fmt.Printf("Feedback records enqueued: %d\n", stats.FeedbackRecordsEnqueued)
	fmt.Printf("Topics enqueued:           %d\n", stats.TopicsEnqueued)
	fmt.Printf("Knowledge records enqueued: %d\n", stats.KnowledgeRecordsEnqueued)
	fmt.Printf("Errors:                    %d\n", stats.Errors)
	fmt.Println()

	total := stats.FeedbackRecordsEnqueued + stats.TopicsEnqueued + stats.KnowledgeRecordsEnqueued
	if total == 0 {
		slog.Info("No records need backfilling")
	} else {
		slog.Info("Jobs enqueued successfully",
			"total", total,
			"feedback", stats.FeedbackRecordsEnqueued,
			"topics", stats.TopicsEnqueued,
			"knowledge", stats.KnowledgeRecordsEnqueued,
		)
		fmt.Println("Jobs have been enqueued. They will be processed by the running API server.")
		fmt.Println("You can also run this command with the API server stopped and wait for completion.")
	}

	// Stop River gracefully
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := riverClient.Stop(shutdownCtx); err != nil {
		slog.Error("Failed to stop River gracefully", "error", err)
	}

	slog.Info("Backfill complete")
}
