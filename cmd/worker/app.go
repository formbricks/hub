package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/llm"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
)

// WorkerApp runs River workers (webhook delivery, embeddings). It does not start an HTTP server.
type WorkerApp struct {
	cfg            *config.Config
	db             *pgxpool.Pool
	river          *river.Client[pgx.Tx]
	embeddingBatch *service.BatchingEmbeddingClient
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
}

// NewWorkerApp builds the River client with all workers and returns an app that runs only River.
func NewWorkerApp(cfg *config.Config, db *pgxpool.Pool) (*WorkerApp, error) {
	var (
		metrics        *observability.Metrics
		meterProvider  *sdkmetric.MeterProvider
		genAIUsage     llm.UsageRecorder
		tracerProvider *sdktrace.TracerProvider
		err            error
	)

	if cfg.Observability.MetricsExporter == "otlp" {
		meterProvider, err = observability.NewMeterProvider(cfg, "hub-worker")
		if err != nil {
			return nil, fmt.Errorf("create meter provider: %w", err)
		}

		if meterProvider != nil {
			metrics, err = observability.NewMetrics(meterProvider.Meter("hub"))
			if err != nil {
				_ = observability.ShutdownMeterProvider(context.Background(), meterProvider)

				return nil, fmt.Errorf("create metrics: %w", err)
			}

			// hub-worker is where the provider calls happen, so it is where their cost is
			// observable. A nil recorder (metrics disabled) leaves the clients behaving exactly
			// as before.
			genAIUsage, err = observability.NewGenAIMetrics(meterProvider.Meter("hub"))
			if err != nil {
				_ = observability.ShutdownMeterProvider(context.Background(), meterProvider)

				return nil, fmt.Errorf("create gen_ai metrics: %w", err)
			}
		}
	}

	if cfg.Observability.TracesExporter != "" {
		tracerProvider, err = observability.NewTracerProvider(cfg, "hub-worker")
		if err != nil {
			if meterProvider != nil {
				_ = observability.ShutdownMeterProvider(context.Background(), meterProvider)
			}

			return nil, fmt.Errorf("create tracer provider: %w", err)
		}
	}

	webhooksRepo := repository.NewWebhooksRepository(db)

	var (
		webhookMetrics     observability.WebhookMetrics
		embeddingMetrics   observability.EmbeddingMetrics
		translationMetrics observability.TranslationMetrics
		sentimentMetrics   observability.SentimentMetrics
		emotionsMetrics    observability.EmotionsMetrics
	)

	if metrics != nil {
		webhookMetrics = metrics.Webhooks
		embeddingMetrics = metrics.Embeddings
		translationMetrics = metrics.Translation
		sentimentMetrics = metrics.Sentiment
		emotionsMetrics = metrics.Emotions
	}

	webhookSender := service.NewWebhookSenderImpl(
		webhooksRepo, webhookMetrics, service.NewSSRFPolicy(cfg.Webhook.URLBlacklist, cfg.Webhook.AllowedCIDRs),
		cfg.Webhook.HTTPTimeout.Duration(), nil)

	// hub-worker performs feedback-records purges; it never enqueues them (the API does), so the
	// service is built without an inserter.
	tenantDataRepo := repository.NewTenantDataRepository(db, cfg.TenantData.PurgeLockTimeout.Duration())
	feedbackRecordsPurgeService := service.NewFeedbackRecordsPurgeService(tenantDataRepo, nil)

	reconcileService := newEnrichmentReconcileService(cfg, db)

	deps := workers.RiverDeps{
		WebhooksRepo:       webhooksRepo,
		WebhookSender:      webhookSender,
		WebhookHTTPTimeout: cfg.Webhook.HTTPTimeout.Duration(),
		WebhookMetrics:     webhookMetrics,

		FeedbackRecordsPurgeService: feedbackRecordsPurgeService,

		// hub-worker is the only process that works enrichment jobs, so it is the only one that
		// can observe a failure and the only one that records them. The backfill commands register
		// workers but never Start() a client, so they never reach this path.
		Failures: repository.NewEnrichmentFailuresRepository(db),
		// The worker is where a terminal give-up is observed, so it is where the counter lives.
		// nil when metrics are disabled, which the worker treats as "do not record".
		FailureMetrics: failureMetrics(metrics),
		// nil when the sweep is switched off or no enrichment is configured, which leaves the
		// reconcile worker and its queue unregistered rather than registered-and-idle.
		ReconcileSweeper: reconcileSweeperOrNil(reconcileService),
		ReconcileMetrics: reconcileMetrics(metrics),
	}

	providerName, embeddingModel := embeddingProviderAndModel(cfg)
	taxonomyEmbeddingModel := service.TaxonomyEmbeddingModel(embeddingModel, cfg.Taxonomy.EmbeddingModel)

	taxonomyEmbeddingEnqueueModel := taxonomyEmbeddingModel

	if providerName == "" {
		taxonomyEmbeddingEnqueueModel = ""
	}

	var (
		translationRecordsService *service.FeedbackRecordsService
		embeddingBatch            *service.BatchingEmbeddingClient
		embeddingReconcileService *service.EmbeddingReconcileService
	)

	if providerName != "" {
		embeddingCfg := service.EmbeddingClientConfig{
			UsageRecorder:         genAIUsage,
			Provider:              providerName,
			ProviderAPIKey:        cfg.Embedding.ProviderAPIKey,
			Model:                 embeddingModel,
			BaseURL:               cfg.Embedding.BaseURL,
			HTTPDisableKeepAlives: cfg.Embedding.HTTPDisableKeepAlives,
			Normalize:             cfg.Embedding.Normalize,
			GoogleCloudProject:    cfg.Embedding.GoogleCloudProject,
			GoogleCloudLocation:   cfg.Embedding.GoogleCloudLocation,
		}
		if err := service.ValidateEmbeddingConfig(embeddingCfg); err != nil {
			shutdownObservability(context.Background(), meterProvider, tracerProvider)

			return nil, fmt.Errorf("embedding config: %w", err)
		}

		slog.Info("embedding worker configured",
			"provider", providerName,
			"http_disable_keep_alives", cfg.Embedding.HTTPDisableKeepAlives,
		)

		embeddingClient, err := service.NewEmbeddingClient(context.Background(), embeddingCfg)
		if err != nil {
			shutdownObservability(context.Background(), meterProvider, tracerProvider)

			return nil, fmt.Errorf("create embedding client: %w", err)
		}

		if batchClient, enabled := service.NewBatchingEmbeddingClient(
			embeddingClient,
			service.EmbeddingBatchConfig{
				BatchSize:   cfg.Embedding.BatchSize,
				MaxWait:     time.Duration(cfg.Embedding.BatchMaxWaitMs) * time.Millisecond,
				MaxInFlight: cfg.Embedding.BatchMaxInFlight,
			},
			embeddingMetrics,
		); enabled {
			embeddingClient = batchClient
			embeddingBatch = batchClient

			slog.Info("embedding document batching enabled",
				"batch_size", cfg.Embedding.BatchSize,
				"max_wait_ms", cfg.Embedding.BatchMaxWaitMs,
				"max_in_flight", cfg.Embedding.BatchMaxInFlight,
			)
		} else if cfg.Embedding.BatchSize > 1 {
			slog.Info("embedding document batching unavailable for provider; using single requests",
				"provider", providerName)
		}

		feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
		embeddingsRepo := repository.NewEmbeddingsRepository(db)
		feedbackRecordsService := service.NewFeedbackRecordsService(
			feedbackRecordsRepo,
			embeddingsRepo,
			embeddingModel,
			nil,
			nil,
			service.EmbeddingsQueueName,
			cfg.Embedding.MaxAttempts,
			"", // translation default unused: this service handles embeddings only
		)
		docPrefix := service.EmbeddingPrefixForProvider(providerName)

		deps.EmbeddingService = feedbackRecordsService
		deps.EmbeddingClient = embeddingClient
		deps.EmbeddingDocPrefix = docPrefix
		deps.EmbeddingMetrics = embeddingMetrics

		if embeddingReconcileConfigured(cfg) {
			embeddingReconcileService = service.NewEmbeddingReconcileService(
				embeddingsRepo,
				taxonomyEmbeddingModel,
				cfg.Embedding.ReconcileTargetDepth,
				cfg.Embedding.MaxAttempts,
				cfg.Embedding.ReconcileRetryAfter.Duration(),
			)
			deps.EmbeddingReconcileSweeper = embeddingReconcileService

			slog.Info("taxonomy embedding reconciliation configured",
				"interval", cfg.Embedding.ReconcileInterval.Duration(),
				"retry_after", cfg.Embedding.ReconcileRetryAfter.Duration(),
				"target_depth", cfg.Embedding.ReconcileTargetDepth,
				"max_concurrent", cfg.Embedding.ReconcileMaxConcurrent,
			)
		}
	}

	if cfg.Translation.Enabled() {
		translationCfg := service.TranslationClientConfig{
			UsageRecorder:       genAIUsage,
			Provider:            cfg.Translation.Provider,
			ProviderAPIKey:      cfg.Translation.ProviderAPIKey,
			Model:               cfg.Translation.Model,
			BaseURL:             cfg.Translation.BaseURL,
			GoogleCloudProject:  cfg.Translation.GoogleCloudProject,
			GoogleCloudLocation: cfg.Translation.GoogleCloudLocation,
		}

		translationClient, err := service.NewTranslationClient(context.Background(), translationCfg)
		if err != nil {
			shutdownObservability(context.Background(), meterProvider, tracerProvider)

			return nil, fmt.Errorf("translation config: %w", err)
		}

		// The translation worker only reads the record and writes the translation, so
		// the raw embedding params are unused here. The taxonomy embedding params let
		// successful translation writes enqueue translated taxonomy re-embedding.
		translationRecordsRepo := repository.NewFeedbackRecordsRepository(db)
		translationRecordsService = service.NewFeedbackRecordsService(
			translationRecordsRepo,
			nil,
			"",
			nil,
			nil,
			service.EmbeddingsQueueName,
			cfg.Embedding.MaxAttempts,
			cfg.Translation.DefaultLanguage,
		)
		translationRecordsService.SetTaxonomyEmbeddingModel(taxonomyEmbeddingEnqueueModel)

		deps.TranslationService = translationRecordsService
		deps.TranslationClient = translationClient
		deps.TranslationMetrics = translationMetrics
		deps.TranslationBackfillService = translationRecordsService
		deps.TranslationMaxAttempts = cfg.Translation.MaxAttempts
	}

	if cfg.Sentiment.Enabled() {
		sentimentClient, err := service.NewSentimentClient(context.Background(), service.SentimentClientConfig{
			UsageRecorder:       genAIUsage,
			Provider:            cfg.Sentiment.Provider,
			ProviderAPIKey:      cfg.Sentiment.ProviderAPIKey,
			Model:               cfg.Sentiment.Model,
			BaseURL:             cfg.Sentiment.BaseURL,
			GoogleCloudProject:  cfg.Sentiment.GoogleCloudProject,
			GoogleCloudLocation: cfg.Sentiment.GoogleCloudLocation,
		})
		if err != nil {
			shutdownObservability(context.Background(), meterProvider, tracerProvider)

			return nil, fmt.Errorf("sentiment config: %w", err)
		}

		// The sentiment worker only reads the record and writes the sentiment, so the
		// embedding/translation-specific service params are unused here.
		sentimentRecordsRepo := repository.NewFeedbackRecordsRepository(db)
		sentimentRecordsService := service.NewFeedbackRecordsService(
			sentimentRecordsRepo, nil, "", nil, nil, "", 0, "")

		// The worker re-checks the per-directory sentiment gate (the enqueue provider fails open on a
		// settings-read error), so it needs its own tenant-settings reader. Read uncached so the gate
		// stays authoritative: a toggle takes effect on the next job, and there is no settings-write
		// cache-eviction hook in this process (writes go through hub-api).
		sentimentSettingsService := service.NewTenantSettingsService(repository.NewTenantSettingsRepository(db))

		deps.SentimentService = sentimentRecordsService
		deps.SentimentResolver = sentimentSettingsService
		deps.SentimentClient = sentimentClient
		deps.SentimentMetrics = sentimentMetrics
	}

	if cfg.Emotions.Enabled() {
		emotionsClient, err := service.NewEmotionsClient(context.Background(), service.EmotionsClientConfig{
			UsageRecorder:       genAIUsage,
			Provider:            cfg.Emotions.Provider,
			ProviderAPIKey:      cfg.Emotions.ProviderAPIKey,
			Model:               cfg.Emotions.Model,
			BaseURL:             cfg.Emotions.BaseURL,
			GoogleCloudProject:  cfg.Emotions.GoogleCloudProject,
			GoogleCloudLocation: cfg.Emotions.GoogleCloudLocation,
		})
		if err != nil {
			shutdownObservability(context.Background(), meterProvider, tracerProvider)

			return nil, fmt.Errorf("emotions config: %w", err)
		}

		// The emotions worker only reads the record and writes the emotions, so the
		// embedding/translation-specific service params are unused here.
		emotionsRecordsRepo := repository.NewFeedbackRecordsRepository(db)
		emotionsRecordsService := service.NewFeedbackRecordsService(
			emotionsRecordsRepo, nil, "", nil, nil, "", 0, "")

		// The worker re-checks the per-directory emotions gate (the enqueue provider fails open on a
		// settings-read error), so it needs its own tenant-settings reader. Read uncached so the gate
		// stays authoritative: a toggle takes effect on the next job, and there is no settings-write
		// cache-eviction hook in this process (writes go through hub-api).
		emotionsSettingsService := service.NewTenantSettingsService(repository.NewTenantSettingsRepository(db))

		deps.EmotionsService = emotionsRecordsService
		deps.EmotionsResolver = emotionsSettingsService
		deps.EmotionsClient = emotionsClient
		deps.EmotionsMetrics = emotionsMetrics
	}

	riverWorkers, queues := workers.NewRiverWorkersAndQueues(cfg, deps)

	riverCfg := &river.Config{
		Queues:  queues,
		Workers: riverWorkers,
	}

	riverCfg.PeriodicJobs = append(riverCfg.PeriodicJobs,
		reconcilePeriodicJobs(cfg, reconcileService != nil, embeddingReconcileService != nil)...)

	if cfg.River.JobTimeoutSec.Duration() > 0 {
		riverCfg.JobTimeout = cfg.River.JobTimeoutSec.Duration()
	}

	if cfg.River.RescueStuckJobsAfterSec.Duration() > 0 {
		riverCfg.RescueStuckJobsAfter = cfg.River.RescueStuckJobsAfterSec.Duration()
	}

	if cfg.River.CompletedJobRetentionSec >= 0 {
		riverCfg.CompletedJobRetentionPeriod = time.Duration(cfg.River.CompletedJobRetentionSec) * time.Second
	} else {
		riverCfg.CompletedJobRetentionPeriod = -1
	}

	if cfg.River.ClientID != "" {
		riverCfg.ID = cfg.River.ClientID
	}

	riverClient, err := river.NewClient(riverpgxv5.New(db), riverCfg)
	if err != nil {
		shutdownObservability(context.Background(), meterProvider, tracerProvider)

		return nil, fmt.Errorf("create River client: %w", err)
	}

	if translationRecordsService != nil {
		translationRecordsService.SetEmbeddingInserter(riverClient)
	}

	if reconcileService != nil {
		reconcileService.SetInserter(riverClient)
	}

	if embeddingReconcileService != nil {
		embeddingReconcileService.SetInserter(riverClient)
	}

	return &WorkerApp{
		cfg:            cfg,
		db:             db,
		river:          riverClient,
		embeddingBatch: embeddingBatch,
		meterProvider:  meterProvider,
		tracerProvider: tracerProvider,
	}, nil
}

// embeddingReconcileConfigured keeps automatic repair aligned with the taxonomy embedding
// backlog contract: embeddings and taxonomy must be configured before the worker spends provider
// calls creating taxonomy-only vectors. Translation is intentionally not a gate because taxonomy
// input falls back to the source text when a deployment does not translate records.
func embeddingReconcileConfigured(cfg *config.Config) bool {
	return cfg.Embedding.ReconcileEnabled &&
		cfg.Embedding.Provider != "" && cfg.Embedding.Model != "" &&
		cfg.Taxonomy.ServiceURL != ""
}

// embeddingProviderAndModel returns (canonical provider, model) when embeddings are enabled
// (provider and model set and supported). Otherwise ("", "").
func embeddingProviderAndModel(cfg *config.Config) (provider, model string) {
	if cfg.Embedding.Provider == "" || cfg.Embedding.Model == "" {
		return "", ""
	}

	providerCanonical := service.NormalizeEmbeddingProvider(cfg.Embedding.Provider)
	if _, ok := service.SupportedEmbeddingProviders()[providerCanonical]; !ok {
		slog.Info("embeddings disabled: unsupported EMBEDDING_PROVIDER",
			"provider", cfg.Embedding.Provider, "model", cfg.Embedding.Model)

		return "", ""
	}

	return providerCanonical, cfg.Embedding.Model
}

// Run starts River and blocks until ctx is cancelled (e.g. SIGINT/SIGTERM), then stops River and returns.
// Uses River's documented pattern: Start() runs workers in background; a goroutine calls Stop() on signal;
// we block on Stopped() so Run() does not return until River has fully shut down.
// See https://riverqueue.com/docs/graceful-shutdown and river.Client.Stopped().
func (a *WorkerApp) Run(ctx context.Context) error {
	if err := a.river.Start(ctx); err != nil {
		return fmt.Errorf("river start: %w", err)
	}

	slog.Info("Worker running", "client_id", a.river.ID())

	go func() {
		<-ctx.Done()
		// Shutdown timeout from a fresh context so Stop() has time to finish; ctx is already cancelled.
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.Server.ShutdownTimeout.Duration())
		defer cancel()

		// River's documented two-phase shutdown: give running jobs the grace period to finish,
		// then escalate to StopAndCancel (cancels job contexts) so the process exits before the
		// orchestrator's SIGKILL. Without the escalation, jobs still running at the deadline were
		// killed mid-flight with their rows left in `running` until the rescuer reclaimed them
		// (~1h by default) — an enrichment latency hole after every unlucky deploy.
		if err := a.river.Stop(stopCtx); err != nil {
			slog.Warn("river graceful stop did not finish in time; cancelling running jobs", "error", err)

			cancelCtx, cancelHard := context.WithTimeout(context.WithoutCancel(ctx), riverStopAndCancelTimeout)
			defer cancelHard()

			_ = a.river.StopAndCancel(cancelCtx)
		}
	}()

	<-a.river.Stopped()

	if a.embeddingBatch != nil {
		batchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), embeddingBatchFlushTimeout)
		defer cancel()

		if err := a.embeddingBatch.Shutdown(batchCtx); err != nil {
			return fmt.Errorf("embedding batch shutdown: %w", err)
		}
	}

	return nil
}

// riverStopAndCancelTimeout bounds the escalated (job-cancelling) stop after the graceful stop
// timed out; kept short so the pod exits within the orchestrator's termination grace period.
const riverStopAndCancelTimeout = 5 * time.Second

// embeddingBatchFlushTimeout bounds the one final partial-batch flush without extending River's
// graceful shutdown by another full server shutdown period.
const embeddingBatchFlushTimeout = 5 * time.Second

func shutdownObservability(ctx context.Context, meter *sdkmetric.MeterProvider, tracer *sdktrace.TracerProvider) {
	if tracer != nil {
		_ = observability.ShutdownTracerProvider(ctx, tracer)
	}

	if meter != nil {
		_ = observability.ShutdownMeterProvider(ctx, meter)
	}
}

// Shutdown stops River and observability.
// River's Stop is idempotent: on normal shutdown, Run's goroutine already calls Stop when ctx is cancelled,
// so Shutdown may call Stop again; that is intentional and safe—do not "fix" by removing this call.
func (a *WorkerApp) Shutdown(ctx context.Context) (err error) {
	if stopErr := a.river.Stop(ctx); stopErr != nil {
		err = fmt.Errorf("river stop: %w", stopErr)
	}

	if a.embeddingBatch != nil {
		if batchErr := a.embeddingBatch.Shutdown(ctx); batchErr != nil {
			if err == nil {
				err = fmt.Errorf("embedding batch shutdown: %w", batchErr)
			} else {
				slog.Error("shutdown embedding batcher", "error", batchErr)
			}
		}
	}

	if a.tracerProvider != nil {
		if obsErr := observability.ShutdownTracerProvider(ctx, a.tracerProvider); obsErr != nil {
			if err == nil {
				err = obsErr
			} else {
				slog.Error("shutdown tracer provider", "error", obsErr)
			}
		}
	}

	if a.meterProvider != nil {
		if obsErr := observability.ShutdownMeterProvider(ctx, a.meterProvider); obsErr != nil {
			if err == nil {
				err = obsErr
			} else {
				slog.Error("shutdown meter provider", "error", obsErr)
			}
		}
	}

	return err
}

// failureMetrics pulls the enrichment failure metrics off the aggregate, tolerating metrics being
// disabled entirely (a nil *Metrics), which is the default for a deployment with no OTLP exporter.
func failureMetrics(metrics *observability.Metrics) observability.EnrichmentFailureMetrics {
	if metrics == nil {
		return nil
	}

	return metrics.EnrichmentFailures
}

// reconcileMetrics is failureMetrics for the sweep's own signals. Same nil-guard: a typed nil
// inside a non-nil interface would pass the worker's "metrics != nil" check and panic on the
// first sweep.
func reconcileMetrics(metrics *observability.Metrics) observability.EnrichmentReconcileMetrics {
	if metrics == nil {
		return nil
	}

	return metrics.EnrichmentReconcile
}

// newEnrichmentReconcileService builds the sweep, or returns nil when it should not run: the kill
// switch is off, or the deployment has no enrichment provider at all and so has nothing to sweep
// for.
func newEnrichmentReconcileService(cfg *config.Config, db *pgxpool.Pool) *service.EnrichmentReconcileService {
	if !cfg.EnrichmentReconcile.Enabled {
		slog.Info("enrichment reconcile: disabled by configuration; enrichment coverage is " +
			"event-driven only and will not self-heal")

		return nil
	}

	// The three record-level enrichments; embeddings has no failure markers and no pending-set
	// query, so it is not swept.
	const recordLevelEnrichments = 3

	specs := make([]service.EnrichmentSweepSpec, 0, recordLevelEnrichments)

	if cfg.Translation.Enabled() {
		specs = append(specs, service.EnrichmentSweepSpec{
			Name: models.EnrichmentNameTranslation, MaxAttempts: cfg.Translation.MaxAttempts,
		})
	}

	if cfg.Sentiment.Enabled() {
		specs = append(specs, service.EnrichmentSweepSpec{
			Name: models.EnrichmentNameSentiment, MaxAttempts: cfg.Sentiment.MaxAttempts,
		})
	}

	if cfg.Emotions.Enabled() {
		specs = append(specs, service.EnrichmentSweepSpec{
			Name: models.EnrichmentNameEmotions, MaxAttempts: cfg.Emotions.MaxAttempts,
		})
	}

	if len(specs) == 0 {
		return nil
	}

	slog.Info("enrichment reconcile: enabled",
		"enrichments", len(specs),
		"interval", cfg.EnrichmentReconcile.Interval(),
		"target_depth", cfg.EnrichmentReconcile.Depth())

	return service.NewEnrichmentReconcileService(service.NewEnrichmentReconcileServiceParams{
		Repo:        repository.NewEnrichmentReconcileRepository(db),
		DefaultLang: cfg.Translation.DefaultLanguage,
		TargetDepth: cfg.EnrichmentReconcile.Depth(),
		Specs:       specs,
	})
}

// reconcileSweeperOrNil converts a nil *EnrichmentReconcileService into a nil interface. Returning
// the typed nil directly would give the worker registry a non-nil interface holding a nil pointer,
// which registers the worker and panics on the first sweep.
func reconcileSweeperOrNil(svc *service.EnrichmentReconcileService) workers.ReconcileSweeper {
	if svc == nil {
		return nil
	}

	return svc
}

// reconcileSweepSchedule is one periodic sweep's schedule, split out so it can be asserted on
// without standing up a River client or a database.
type reconcileSweepSchedule struct {
	interval time.Duration
	args     river.JobArgs
	opts     *river.InsertOpts
}

// reconcileSweepSchedules returns the schedules for the sweeps that are enabled.
//
// This exists as its own function because the two sweeps are independent and appended to the same
// slice, which is a shape that loses work silently: assign instead of append, or resolve a merge
// by keeping one side, and one reconciler simply never runs. Nothing about that fails — it builds,
// every test passes, and the only symptom is coverage quietly not converging weeks later. Keeping
// the selection here means a test can hold both sweeps to being present.
//
// River runs periodic jobs from the ELECTED LEADER only, which is the whole reason the sweeps are
// scheduled this way rather than by a ticker in this process: with several hub-workers deployed, a
// ticker would have every replica sweeping at once.
//
// The scheduler keeps its state in memory, so a restart or a leader change starts the cycle over.
// That costs nothing here — the sweeps are level-triggered, so a missed tick is invisible and an
// extra one finds a queue already at depth and does nothing. RunOnStart is on for the same reason:
// after a deploy, converging sooner is strictly better than waiting out a full interval.
func reconcileSweepSchedules(
	cfg *config.Config, enrichmentSweep, embeddingSweep bool,
) []reconcileSweepSchedule {
	var schedules []reconcileSweepSchedule

	if enrichmentSweep {
		schedules = append(schedules, reconcileSweepSchedule{
			interval: cfg.EnrichmentReconcile.Interval(),
			args:     service.EnrichmentReconcileArgs{},
			opts: &river.InsertOpts{
				Queue: service.EnrichmentReconcileQueueName,
				// ONE attempt, deliberately. A failed sweep needs no retry — the next scheduled
				// tick IS the retry, and it sees the same backlog. With River's default 25
				// attempts a failing sweep's backoff grows past the tick interval, and because the
				// retryable job holds the unique key, every subsequent tick would be skipped as a
				// duplicate: a DB blip would then silence reconciliation for hours, not minutes.
				MaxAttempts: 1,
				// Uniqueness across the in-flight states, so a tick arriving while the previous
				// sweep is still running collapses into it instead of queueing a second scan.
				UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: service.InFlightUniqueStates()},
			},
		})
	}

	if embeddingSweep {
		schedules = append(schedules, reconcileSweepSchedule{
			interval: cfg.Embedding.ReconcileInterval.Duration(),
			args:     service.EmbeddingReconcileArgs{},
			opts: &river.InsertOpts{
				Queue:       service.EmbeddingReconcileQueueName,
				MaxAttempts: 1,
				// The same in-flight set the enrichment sweep uses, via the shared helper rather
				// than a second copy: the helper's doc explains why getting this set wrong is
				// silent in both directions.
				UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: service.InFlightUniqueStates()},
			},
		})
	}

	return schedules
}

// reconcilePeriodicJobs turns the enabled schedules into River periodic jobs.
func reconcilePeriodicJobs(
	cfg *config.Config, enrichmentSweep, embeddingSweep bool,
) []*river.PeriodicJob {
	schedules := reconcileSweepSchedules(cfg, enrichmentSweep, embeddingSweep)
	jobs := make([]*river.PeriodicJob, 0, len(schedules))

	for _, schedule := range schedules {
		jobs = append(jobs, river.NewPeriodicJob(
			river.PeriodicInterval(schedule.interval),
			func() (river.JobArgs, *river.InsertOpts) { return schedule.args, schedule.opts },
			&river.PeriodicJobOpts{RunOnStart: true},
		))
	}

	return jobs
}
