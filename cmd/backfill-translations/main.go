// backfill-translations enqueues River translation jobs for feedback records whose
// tenant has a target language configured and whose value_text is not yet translated
// to it (missing or stale). Run this one-off after enabling translation or changing a
// tenant's target language; hub-worker (or the API process) runs the jobs.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
	"github.com/formbricks/hub/pkg/database"
)

var (
	errTranslationProviderRequired = errors.New("TRANSLATION_PROVIDER is required")
	errTranslationModelRequired    = errors.New("TRANSLATION_MODEL is required")
)

const (
	defaultTranslationMaxAttempts = 3
	exitSuccess                   = 0
	exitFailure                   = 1
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

	if cfg.Translation.Provider == "" {
		slog.Error(errTranslationProviderRequired.Error())

		return exitFailure
	}

	if cfg.Translation.Model == "" {
		slog.Error(errTranslationModelRequired.Error())

		return exitFailure
	}

	maxAttempts := cfg.Translation.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultTranslationMaxAttempts
	}

	ctx := context.Background()

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
	)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)

		return exitFailure
	}
	defer db.Close()

	translationCfg := service.TranslationClientConfig{
		Provider:            cfg.Translation.Provider,
		ProviderAPIKey:      cfg.Translation.ProviderAPIKey,
		Model:               cfg.Translation.Model,
		BaseURL:             cfg.Translation.BaseURL,
		GoogleCloudProject:  cfg.Translation.GoogleCloudProject,
		GoogleCloudLocation: cfg.Translation.GoogleCloudLocation,
	}

	translationClient, err := service.NewTranslationClient(ctx, translationCfg)
	if err != nil {
		slog.Error("Failed to create translation client", "error", err)

		return exitFailure
	}

	repo := repository.NewFeedbackRecordsRepository(db)
	feedbackRecordsService := service.NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0)

	// Producer-only: we only enqueue jobs; workers run in hub-worker (or the API process).
	// River requires the job kind registered (worker added) and MaxWorkers > 0 for a declared queue.
	riverWorkers := river.NewWorkers()
	river.AddWorker(riverWorkers, workers.NewFeedbackTranslationWorker(feedbackRecordsService, translationClient, nil))

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues: map[string]river.QueueConfig{
			service.TranslationsQueueName: {MaxWorkers: 1},
		},
		Workers: riverWorkers,
	})
	if err != nil {
		slog.Error("Failed to create River client", "error", err)

		return exitFailure
	}

	enqueued, err := feedbackRecordsService.BackfillTranslations(
		ctx, riverClient, service.TranslationsQueueName, maxAttempts)
	if err != nil {
		slog.Error("Backfill failed", "error", err)

		return exitFailure
	}

	slog.Info("Backfill complete", "enqueued", enqueued)

	fmt.Printf("Enqueued %d translation job(s).\n", enqueued)

	return exitSuccess
}
