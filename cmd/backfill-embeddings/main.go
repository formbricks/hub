// backfill-embeddings enqueues River embedding jobs for the feedback records whose embedding is
// missing, stale or orphaned: the same selection as the repair sweep (EMBEDDING_RECONCILE_*), on
// demand and at full worker concurrency, for incident recovery or a deployment without the sweep.
// hub-worker processes the jobs.
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
	countOnly := flag.Bool("count-only", false, "count records owed an embedding without enqueueing jobs")
	maxRecords := flag.Int("max-records", 0, "maximum candidates to enqueue; 0 means unlimited")
	stale := flag.Bool("stale", false,
		"also re-embed vectors written before Hub recorded what they were computed from (requires -tenant-id)")
	retryTerminal := flag.Bool("retry-terminal", false,
		"also retry records the provider rejected outright, once the cause is fixed (requires -tenant-id)")
	cooldown := flag.Duration("cooldown", 0,
		"skip records whose attempts ran out on a transient error less than this long ago (e.g. 24h to keep "+
			"a record that keeps failing out of repeated batch runs); 0 retries them at once")

	flag.Parse()

	if *maxRecords < 0 {
		slog.Error("-max-records cannot be negative")

		return exitFailure
	}

	if *cooldown < 0 {
		slog.Error("-cooldown cannot be negative")

		return exitFailure
	}

	if (*stale || *retryTerminal) && *tenantID == "" {
		slog.Error("-stale and -retry-terminal require -tenant-id: they can select every vector a tenant has")

		return exitFailure
	}

	if *pruneStaleModels &&
		(*taxonomyMode || *tenantID != "" || *countOnly || *maxRecords != 0 || *stale || *retryTerminal) {
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
		Provider:              providerCanonical,
		ProviderAPIKey:        cfg.Embedding.ProviderAPIKey,
		Model:                 embeddingModel,
		BaseURL:               cfg.Embedding.BaseURL,
		HTTPDisableKeepAlives: cfg.Embedding.HTTPDisableKeepAlives,
		Normalize:             cfg.Embedding.Normalize,
		GoogleCloudProject:    cfg.Embedding.GoogleCloudProject,
		GoogleCloudLocation:   cfg.Embedding.GoogleCloudLocation,
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

	if *tenantID != "" {
		tenantExists, tenantErr := embeddingsRepo.TenantExistsForEmbeddingBackfill(ctx, *tenantID)
		if tenantErr != nil {
			slog.Error("Backfill tenant validation failed", "error", tenantErr, "tenant_id", *tenantID)

			return exitFailure
		}

		if !tenantExists {
			slog.Error("No Hub data found for tenant; check -tenant-id", "tenant_id", *tenantID)

			return exitFailure
		}
	}

	params := service.EmbeddingBackfillParams{
		Model:           targetModel,
		InputKind:       inputKind,
		TenantID:        *tenantID,
		MaxRecords:      *maxRecords,
		IncludeLegacy:   *stale,
		IncludeTerminal: *retryTerminal,
		Cooldown:        *cooldown,
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

	// The Hub has no audit store; this line and the completion line are the record of who changed
	// what (the operator's exec session is the who). Records are identified by count, never content.
	scope := []any{
		"model", targetModel, "input_kind", inputKind, "tenant_id", *tenantID,
		"max_records", *maxRecords, "stale", *stale, "retry_terminal", *retryTerminal, "cooldown", *cooldown,
		"count_only", *countOnly,
	}
	slog.Info("Embedding backfill started", scope...) // #nosec G706 -- values are structured log attributes

	if *countOnly {
		count, countErr := feedbackRecordsService.CountEmbeddingBackfill(ctx, params)
		if countErr != nil {
			slog.Error("Backfill count failed", "error", countErr)

			return exitFailure
		}

		selected := count
		if *maxRecords > 0 && selected > *maxRecords {
			selected = *maxRecords
		}

		fmt.Printf("Found %d record(s) owed an embedding for model %q; %d would be selected.\n",
			count, targetModel, selected)

		return exitSuccess
	}

	embeddingClient, err := service.NewEmbeddingClient(ctx, embeddingCfg)
	if err != nil {
		slog.Error("Failed to create embedding client", "error", err)

		return exitFailure
	}

	docPrefix := service.EmbeddingPrefixForProvider(providerCanonical)
	embeddingWorker := workers.NewFeedbackEmbeddingWorkerWithOptions(
		feedbackRecordsService,
		embeddingClient,
		docPrefix,
		nil,
		cfg.Embedding.JobTimeout.Duration(),
		nil,
		nil,
		cfg.Embedding.MaxInputBytes,
		nil,
	)
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

	result, err := feedbackRecordsService.BackfillEmbeddings(ctx, params)
	if err != nil {
		slog.Error("Backfill failed", append(scope, "error", err,
			"selected_before_failure", result.Selected, "enqueued_before_failure", result.Enqueued)...)

		return exitFailure
	}

	// #nosec G706 -- values are structured log attributes
	slog.Info("Embedding backfill complete", append(scope, "selected", result.Selected, "enqueued", result.Enqueued)...)

	fmt.Printf("Selected %d record(s) and enqueued %d embedding job(s) for model %q; the rest were already queued.\n",
		result.Selected, result.Enqueued, targetModel)

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
