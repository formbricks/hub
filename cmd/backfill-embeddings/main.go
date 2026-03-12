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

	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
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
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)

		return exitFailure
	}

	if cfg.Database.URL == "" || cfg.Database.URL == config.DefaultDatabaseURL {
		slog.Error("DATABASE_URL must be set explicitly for this binary (do not use the default test URL)")

		return exitFailure
	}

	maxAttempts := cfg.Embedding.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultEmbeddingMaxAttempts
	}

	ctx := context.Background()

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
		database.WithAfterConnect(pgxvec.RegisterTypes),
	)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)

		return exitFailure
	}
	defer db.Close()

	provider, embeddingModel, err := getEmbeddingProviderAndModel(cfg)
	if err != nil {
		slog.Error(err.Error())

		return exitFailure
	}

	providerCanonical := service.NormalizeEmbeddingProvider(provider)
	if _, ok := service.SupportedEmbeddingProviders()[providerCanonical]; !ok {
		slog.Error("unsupported embedding provider", "provider", provider)

		return exitFailure
	}

	embeddingCfg := service.EmbeddingClientConfig{
		Provider:            providerCanonical,
		ProviderAPIKey:      cfg.Embedding.ProviderAPIKey,
		Model:               embeddingModel,
		Normalize:           cfg.Embedding.Normalize,
		GoogleCloudProject:  cfg.Embedding.GoogleCloudProject,
		GoogleCloudLocation: cfg.Embedding.GoogleCloudLocation,
	}
	if err := service.ValidateEmbeddingConfig(embeddingCfg); err != nil {
		slog.Error(err.Error())

		return exitFailure
	}

	embeddingModelForDB := embeddingModel

	repo := repository.NewFeedbackRecordsRepository(db)
	embeddingsRepo := repository.NewEmbeddingsRepository(db)

	feedbackRecordsService := service.NewFeedbackRecordsService(
		repo,
		embeddingsRepo,
		embeddingModelForDB,
		nil,
		nil, // inserter set below after River client is created
		service.EmbeddingsQueueName,
		maxAttempts,
	)

	embeddingClient, err := service.NewEmbeddingClient(ctx, embeddingCfg)
	if err != nil {
		slog.Error("Failed to create embedding client", "error", err)

		return exitFailure
	}

	docPrefix := service.EmbeddingPrefixForProvider(providerCanonical)
	embeddingWorker := workers.NewFeedbackEmbeddingWorker(feedbackRecordsService, embeddingClient, docPrefix, nil)
	riverWorkers := river.NewWorkers()
	river.AddWorker(riverWorkers, embeddingWorker)

	// Producer-only: we only enqueue jobs; workers run in hub-worker. River requires the job kind
	// to be registered (worker added above) and MaxWorkers > 0 when a queue is declared.
	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues: map[string]river.QueueConfig{
			service.EmbeddingsQueueName: {MaxWorkers: 1},
		},
		Workers: riverWorkers,
	})
	if err != nil {
		slog.Error("Failed to create River client", "error", err)

		return exitFailure
	}

	feedbackRecordsService.SetEmbeddingInserter(riverClient)

	enqueued, err := feedbackRecordsService.BackfillEmbeddings(ctx, embeddingModelForDB)
	if err != nil {
		slog.Error("Backfill failed", "error", err)

		return exitFailure
	}

	slog.Info("Backfill complete", "enqueued", enqueued) // #nosec G706 -- enqueued is an int, not user input

	fmt.Printf("Enqueued %d embedding job(s).\n", enqueued)

	return exitSuccess
}

func getEmbeddingProviderAndModel(cfg *config.Config) (provider, model string, err error) {
	provider = cfg.Embedding.Provider
	if provider == "" {
		return "", "", errEmbeddingProviderRequired
	}

	model = cfg.Embedding.Model
	if model == "" {
		return "", "", errEmbeddingModelRequired
	}

	return provider, model, nil
}
