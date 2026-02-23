// backfill-embeddings enqueues River embedding jobs for feedback records that have
// non-empty value_text and null embedding. Run this when the API server is not
// handling backfill (e.g. one-off or scheduled). Workers in the API process the jobs.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
)

const (
	defaultEmbeddingMaxAttempts = 3
	exitSuccess                 = 0
	exitFailure                 = 1
)

func main() {
	os.Exit(run())
}

func run() int {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		slog.Error("DATABASE_URL is required")

		return exitFailure
	}

	maxAttempts := getEnvAsInt("EMBEDDING_MAX_ATTEMPTS", defaultEmbeddingMaxAttempts)
	if maxAttempts <= 0 {
		maxAttempts = defaultEmbeddingMaxAttempts
	}

	ctx := context.Background()

	db, err := database.NewPostgresPool(ctx, databaseURL, database.WithAfterConnect(pgxvec.RegisterTypes))
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)

		return exitFailure
	}
	defer db.Close()

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues: map[string]river.QueueConfig{
			service.EmbeddingsQueueName: {},
		},
		Workers: river.NewWorkers(),
	})
	if err != nil {
		slog.Error("Failed to create River client", "error", err)

		return exitFailure
	}

	repo := repository.NewFeedbackRecordsRepository(db)
	feedbackRecordsService := service.NewFeedbackRecordsService(
		repo,
		nil,
		riverClient,
		service.EmbeddingsQueueName,
		maxAttempts,
	)

	enqueued, err := feedbackRecordsService.BackfillEmbeddings(ctx)
	if err != nil {
		slog.Error("Backfill failed", "error", err)

		return exitFailure
	}

	slog.Info("Backfill complete", "enqueued", enqueued)

	_, _ = fmt.Fprintf(os.Stdout, "Enqueued %d embedding job(s).\n", enqueued)

	return exitSuccess
}

func getEnvAsInt(key string, defaultValue int) int {
	s := os.Getenv(key)
	if s == "" {
		return defaultValue
	}

	n, err := strconv.Atoi(s)
	if err != nil {
		return defaultValue
	}

	return n
}
