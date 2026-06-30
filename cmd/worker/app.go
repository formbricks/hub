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
		meterProvider, err = observability.NewMeterProvider(cfg)
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
		tracerProvider, err = observability.NewTracerProvider(cfg)
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
	)

	if metrics != nil {
		webhookMetrics = metrics.Webhooks
		embeddingMetrics = metrics.Embeddings
		translationMetrics = metrics.Translation
		sentimentMetrics = metrics.Sentiment
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
		// the embedding-specific service params are unused here.
		translationRecordsRepo := repository.NewFeedbackRecordsRepository(db)
		translationRecordsService := service.NewFeedbackRecordsService(
			translationRecordsRepo, nil, "", nil, nil, "", 0, cfg.Translation.DefaultLanguage)

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

		deps.SentimentService = sentimentRecordsService
		deps.SentimentClient = sentimentClient
		deps.SentimentMetrics = sentimentMetrics
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

		_ = a.river.Stop(stopCtx)
	}()

	<-a.river.Stopped()

	return nil
}

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
