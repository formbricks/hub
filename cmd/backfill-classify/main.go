// backfill-classify enqueues River enrichment jobs for feedback records that are missing a
// classification: sentiment (sentiment IS NULL) or emotions (emotions IS NULL). Run this one-off
// after enabling SENTIMENT_* / EMOTIONS_* on a deployment, or to re-enrich a backlog; hub-worker
// (or the API process) runs the enqueued jobs. Select the type with -type sentiment|emotions.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
	"github.com/formbricks/hub/pkg/database"
)

const (
	defaultClassifyMaxAttempts = 3
	exitSuccess                = 0
	exitFailure                = 1
)

// classifyBackfillFunc enqueues an enrichment backfill and returns the number of jobs enqueued.
// FeedbackRecordsService.BackfillSentiment / BackfillEmotions both satisfy it.
type classifyBackfillFunc func(
	ctx context.Context, inserter service.RiverJobInserter, queueName string, maxAttempts int, runID string,
) (int, error)

func main() {
	os.Exit(run())
}

func run() int {
	enrichType := flag.String("type", "", "enrichment type to backfill: sentiment | emotions")

	flag.Parse()

	if *enrichType != "sentiment" && *enrichType != "emotions" {
		slog.Error("invalid -type; must be sentiment or emotions", "type", *enrichType)

		return exitFailure
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)

		return exitFailure
	}

	if cfg.Database.URL == "" || cfg.Database.URL == config.DefaultDatabaseURL {
		slog.Error("DATABASE_URL must be set explicitly for this binary (do not use the default test URL)")

		return exitFailure
	}

	ctx := context.Background()

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)

		return exitFailure
	}
	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)
	feedbackRecordsService := service.NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")
	// The worker is registered producer-only (for kind/queue validation at insert time) and never
	// Start()ed, so its settings resolver is never invoked here; pass a real one anyway.
	settingsService := service.NewTenantSettingsService(repository.NewTenantSettingsRepository(db))

	// Producer-only: we only enqueue jobs; workers run in hub-worker (or the API process). River
	// requires the job kind registered (worker added) and MaxWorkers > 0 for a declared queue. The
	// two enrichment workers are distinct generic types, so each case registers its own.
	riverWorkers := river.NewWorkers()

	var (
		queueName   string
		maxAttempts int
		runBackfill classifyBackfillFunc
	)

	switch *enrichType {
	case "sentiment":
		if cfg.Sentiment.Provider == "" || cfg.Sentiment.Model == "" {
			slog.Error("sentiment is not configured (SENTIMENT_PROVIDER and SENTIMENT_MODEL required)")

			return exitFailure
		}

		client, clientErr := service.NewSentimentClient(ctx, service.SentimentClientConfig{
			Provider:            cfg.Sentiment.Provider,
			ProviderAPIKey:      cfg.Sentiment.ProviderAPIKey,
			Model:               cfg.Sentiment.Model,
			BaseURL:             cfg.Sentiment.BaseURL,
			GoogleCloudProject:  cfg.Sentiment.GoogleCloudProject,
			GoogleCloudLocation: cfg.Sentiment.GoogleCloudLocation,
		})
		if clientErr != nil {
			slog.Error("Failed to create sentiment client", "error", clientErr)

			return exitFailure
		}

		river.AddWorker(riverWorkers, workers.NewFeedbackSentimentWorker(feedbackRecordsService, settingsService, client, nil))

		queueName = service.SentimentsQueueName
		maxAttempts = classifyMaxAttempts(cfg.Sentiment.MaxAttempts)
		runBackfill = feedbackRecordsService.BackfillSentiment
	case "emotions":
		if cfg.Emotions.Provider == "" || cfg.Emotions.Model == "" {
			slog.Error("emotions is not configured (EMOTIONS_PROVIDER and EMOTIONS_MODEL required)")

			return exitFailure
		}

		client, clientErr := service.NewEmotionsClient(ctx, service.EmotionsClientConfig{
			Provider:            cfg.Emotions.Provider,
			ProviderAPIKey:      cfg.Emotions.ProviderAPIKey,
			Model:               cfg.Emotions.Model,
			BaseURL:             cfg.Emotions.BaseURL,
			GoogleCloudProject:  cfg.Emotions.GoogleCloudProject,
			GoogleCloudLocation: cfg.Emotions.GoogleCloudLocation,
		})
		if clientErr != nil {
			slog.Error("Failed to create emotions client", "error", clientErr)

			return exitFailure
		}

		river.AddWorker(riverWorkers, workers.NewFeedbackEmotionsWorker(feedbackRecordsService, settingsService, client, nil))

		queueName = service.EmotionsQueueName
		maxAttempts = classifyMaxAttempts(cfg.Emotions.MaxAttempts)
		runBackfill = feedbackRecordsService.BackfillEmotions
	}

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues:  map[string]river.QueueConfig{queueName: {MaxWorkers: 1}},
		Workers: riverWorkers,
	})
	if err != nil {
		slog.Error("Failed to create River client", "error", err)

		return exitFailure
	}

	// A fresh run id per invocation: a re-run is a new fan-out and must not be swallowed by a
	// previous run's completed jobs (River's unique states include completed).
	runID := uuid.NewString()

	enqueued, err := runBackfill(ctx, riverClient, queueName, maxAttempts, runID)
	if err != nil {
		slog.Error("Backfill failed", "type", *enrichType, "error", err)

		return exitFailure
	}

	slog.Info("Backfill complete", "type", *enrichType, "enqueued", enqueued)
	fmt.Printf("Enqueued %d %s job(s).\n", enqueued, *enrichType)

	return exitSuccess
}

// classifyMaxAttempts returns the configured max attempts, or the default when unset (<= 0).
func classifyMaxAttempts(configured int) int {
	if configured <= 0 {
		return defaultClassifyMaxAttempts
	}

	return configured
}
