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
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
}

// NewWorkerApp builds the River client with all workers and returns an app that runs only River.
func NewWorkerApp(cfg *config.Config, db *pgxpool.Pool) (*WorkerApp, error) {
	var (
		metrics        *observability.Metrics
		meterProvider  *sdkmetric.MeterProvider
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
		webhooksRepo, webhookMetrics, cfg.Webhook.URLBlacklist, cfg.Webhook.HTTPTimeout.Duration(), nil)

	deps := workers.RiverDeps{
		WebhooksRepo:       webhooksRepo,
		WebhookSender:      webhookSender,
		WebhookHTTPTimeout: cfg.Webhook.HTTPTimeout.Duration(),
		WebhookMetrics:     webhookMetrics,
	}

	providerName, embeddingModel := embeddingProviderAndModel(cfg)
	taxonomyEmbeddingModel := service.TaxonomyEmbeddingModel(embeddingModel, cfg.Taxonomy.EmbeddingModel)

	taxonomyEmbeddingEnqueueModel := taxonomyEmbeddingModel

	if providerName == "" {
		taxonomyEmbeddingEnqueueModel = ""
	}

	var translationRecordsService *service.FeedbackRecordsService

	if providerName != "" {
		embeddingCfg := service.EmbeddingClientConfig{
			Provider:            providerName,
			ProviderAPIKey:      cfg.Embedding.ProviderAPIKey,
			Model:               embeddingModel,
			BaseURL:             cfg.Embedding.BaseURL,
			Normalize:           cfg.Embedding.Normalize,
			GoogleCloudProject:  cfg.Embedding.GoogleCloudProject,
			GoogleCloudLocation: cfg.Embedding.GoogleCloudLocation,
		}
		if err := service.ValidateEmbeddingConfig(embeddingCfg); err != nil {
			shutdownObservability(context.Background(), meterProvider, tracerProvider)

			return nil, fmt.Errorf("embedding config: %w", err)
		}

		embeddingClient, err := service.NewEmbeddingClient(context.Background(), embeddingCfg)
		if err != nil {
			shutdownObservability(context.Background(), meterProvider, tracerProvider)

			return nil, fmt.Errorf("create embedding client: %w", err)
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
	}

	if cfg.Translation.Provider != "" && cfg.Translation.Model != "" {
		translationCfg := service.TranslationClientConfig{
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

	riverWorkers, queues := workers.NewRiverWorkersAndQueues(cfg, deps, 0)

	riverCfg := &river.Config{
		Queues:  queues,
		Workers: riverWorkers,
	}
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

	return &WorkerApp{
		cfg:            cfg,
		db:             db,
		river:          riverClient,
		meterProvider:  meterProvider,
		tracerProvider: tracerProvider,
	}, nil
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

	return nil
}

// riverStopAndCancelTimeout bounds the escalated (job-cancelling) stop after the graceful stop
// timed out; kept short so the pod exits within the orchestrator's termination grace period.
const riverStopAndCancelTimeout = 5 * time.Second

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
