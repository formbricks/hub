// backfill-embeddings enqueues River embedding jobs for feedback records that have
// non-empty value_text and null embedding. Run this when the API server is not
// handling backfill (e.g. one-off or scheduled). Workers in the API process the jobs.
package main

import (
	"context"
	"errors"
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

var (
	errEmbeddingProviderRequired = errors.New("EMBEDDING_PROVIDER is required")
	errEmbeddingModelRequired    = errors.New("EMBEDDING_MODEL is required")
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

	embeddingProvider, embeddingModel, err := getEmbeddingProviderAndModel()
	if err != nil {
		slog.Error(err.Error())

		return exitFailure
	}

	_ = embeddingProvider // reserved for future use (e.g. provider-specific backfill)
	repo := repository.NewFeedbackRecordsRepository(db)
	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	feedbackRecordsService := service.NewFeedbackRecordsService(
		repo,
		embeddingsRepo,
		embeddingModel,
		nil,
		riverClient,
		service.EmbeddingsQueueName,
		maxAttempts,
	)

	enqueued, err := feedbackRecordsService.BackfillEmbeddings(ctx, embeddingModel)
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

func getEmbeddingProviderAndModel() (provider, model string, err error) {
	provider = os.Getenv("EMBEDDING_PROVIDER")
	if provider == "" {
		return "", "", errEmbeddingProviderRequired
	}

	model = os.Getenv("EMBEDDING_MODEL")
	if model == "" {
		return "", "", errEmbeddingModelRequired
	}

	return provider, model, nil
}
