// backfill-embeddings enqueues River embedding jobs for feedback records that have
// non-empty value_text and null embedding. Run this when the API server is not
// handling backfill (e.g. one-off or scheduled). hub-worker processes the jobs.
//
// With -prune-stale-models it instead deletes embedding rows left behind by previous
// EMBEDDING_MODEL values. Run the prune only AFTER a model migration's backfill has
// completed (stale rows are invisible to reads but bloat the shared HNSW index).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
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
	// pruneBatchSize bounds each DELETE while pruning stale-model rows, so a large prune
	// never holds long row locks or produces one giant WAL burst.
	pruneBatchSize = 5000
)

func main() {
	os.Exit(run())
}

func run() int {
	pruneStaleModels := flag.Bool("prune-stale-models", false,
		"delete embedding rows whose model differs from EMBEDDING_MODEL instead of backfilling "+
			"(run only after a model migration's backfill has completed)")
	taxonomyMode := flag.Bool("taxonomy", false,
		"backfill taxonomy embeddings from translated text using TAXONOMY_EMBEDDING_MODEL or taxonomy:<EMBEDDING_MODEL>:translated-v1")
	tenantID := flag.String("tenant-id", "", "restrict the backfill to one tenant ID")
	countOnly := flag.Bool("count-only", false, "count eligible missing embeddings without enqueueing jobs")
	maxRecords := flag.Int("max-records", 0, "maximum candidates to enqueue; 0 means unlimited")

	flag.Parse()

	if *maxRecords < 0 {
		slog.Error("-max-records cannot be negative")

		return exitFailure
	}

	if *pruneStaleModels && (*taxonomyMode || *tenantID != "" || *countOnly || *maxRecords != 0) {
		slog.Error("-prune-stale-models cannot be combined with backfill scope or preview flags")

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
		BaseURL:             cfg.Embedding.BaseURL,
		Normalize:           cfg.Embedding.Normalize,
		GoogleCloudProject:  cfg.Embedding.GoogleCloudProject,
		GoogleCloudLocation: cfg.Embedding.GoogleCloudLocation,
	}
	if err := service.ValidateEmbeddingConfig(embeddingCfg); err != nil {
		slog.Error(err.Error())

		return exitFailure
	}

	embeddingModelForDB := embeddingModel
	taxonomyEmbeddingModel := service.TaxonomyEmbeddingModel(embeddingModelForDB, cfg.Taxonomy.EmbeddingModel)
	targetModel := embeddingModelForDB
	inputKind := models.EmbeddingInputKindRaw

	if *taxonomyMode {
		targetModel = taxonomyEmbeddingModel
		inputKind = models.EmbeddingInputKindTaxonomyTranslated
	}

	repo := repository.NewFeedbackRecordsRepository(db)
	embeddingsRepo := repository.NewEmbeddingsRepository(db)

	if *pruneStaleModels {
		deleted, pruneErr := embeddingsRepo.DeleteEmbeddingsForOtherModels(
			ctx, embeddingModelForDB, pruneBatchSize, taxonomyEmbeddingModel)
		if pruneErr != nil {
			slog.Error("Prune failed", "error", pruneErr, "deleted_before_failure", deleted)

			return exitFailure
		}

		slog.Info("Prune complete", "deleted", deleted, "kept_models", []string{embeddingModelForDB, taxonomyEmbeddingModel})
		fmt.Printf("Deleted %d stale-model embedding row(s); kept models %q and %q.\n",
			deleted, embeddingModelForDB, taxonomyEmbeddingModel)

		return exitSuccess
	}

	if *countOnly {
		var count int
		if *tenantID == "" {
			count, err = embeddingsRepo.CountFeedbackRecordsForBackfillByInputKind(ctx, targetModel, inputKind)
		} else {
			count, err = embeddingsRepo.CountTenantFeedbackRecordsForBackfillByInputKind(
				ctx, *tenantID, targetModel, inputKind)
		}

		if err != nil {
			slog.Error("Backfill count failed", "error", err)

			return exitFailure
		}

		selected := count
		if *maxRecords > 0 && selected > *maxRecords {
			selected = *maxRecords
		}

		fmt.Printf("Found %d eligible missing embedding(s) for model %q; %d would be selected.\n",
			count, targetModel, selected)

		return exitSuccess
	}

	feedbackRecordsService := service.NewFeedbackRecordsService(
		repo,
		embeddingsRepo,
		embeddingModelForDB,
		nil,
		nil, // inserter set below after River client is created
		service.EmbeddingsQueueName,
		maxAttempts,
		"", // translation default unused: embeddings backfill only
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

	var enqueued int
	if *tenantID == "" {
		enqueued, err = feedbackRecordsService.BackfillEmbeddingsWithInputKindLimit(
			ctx, targetModel, inputKind, *maxRecords)
	} else {
		enqueued, err = feedbackRecordsService.BackfillTenantEmbeddingsWithInputKind(
			ctx, *tenantID, targetModel, inputKind, *maxRecords)
	}

	if err != nil {
		slog.Error("Backfill failed", "error", err)

		return exitFailure
	}

	logAttrs := []any{
		"enqueued", enqueued,
		"model", targetModel,
		"input_kind", inputKind,
		"tenant_scoped", *tenantID != "",
		"max_records", *maxRecords,
	}
	if *tenantID != "" {
		logAttrs = append(logAttrs, "tenant_id", *tenantID)
	}

	slog.Info("Backfill complete", logAttrs...) // #nosec G706 -- values are structured log attributes

	fmt.Printf("Enqueued %d embedding job(s) for model %q.\n", enqueued, targetModel)

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
