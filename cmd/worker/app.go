package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/googleai"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/openai"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
)

const (
	embeddingProviderOpenAI = "openai"
	embeddingProviderGoogle = "google"
)

var supportedEmbeddingProviders = map[string]struct{}{
	embeddingProviderOpenAI: {},
	embeddingProviderGoogle: {},
}

var (
	errEmbeddingAPIKeyRequired      = errors.New("EMBEDDING_PROVIDER_API_KEY is required for this provider")
	errUnsupportedEmbeddingProvider = errors.New("unsupported embedding provider")
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
		webhookMetrics   observability.WebhookMetrics
		embeddingMetrics observability.EmbeddingMetrics
	)

	if metrics != nil {
		webhookMetrics = metrics.Webhooks
		embeddingMetrics = metrics.Embeddings
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
		if (providerName == embeddingProviderOpenAI || providerName == embeddingProviderGoogle) &&
			cfg.Embedding.ProviderAPIKey == "" {
			shutdownObservability(context.Background(), meterProvider, tracerProvider)

			return nil, fmt.Errorf("%w: %s", errEmbeddingAPIKeyRequired, providerName)
		}

		var embeddingClient service.EmbeddingClient

		switch providerName {
		case embeddingProviderOpenAI:
			embeddingClient = openai.NewClient(cfg.Embedding.ProviderAPIKey,
				openai.WithModel(embeddingModel),
				openai.WithNormalize(cfg.Embedding.Normalize),
			)
		case embeddingProviderGoogle:
			googleClient, err := googleai.NewClient(context.Background(), cfg.Embedding.ProviderAPIKey,
				googleai.WithModel(embeddingModel),
				googleai.WithNormalize(cfg.Embedding.Normalize),
			)
			if err != nil {
				shutdownObservability(context.Background(), meterProvider, tracerProvider)

				return nil, fmt.Errorf("create google embedding client: %w", err)
			}

			embeddingClient = googleClient
		default:
			shutdownObservability(context.Background(), meterProvider, tracerProvider)

			return nil, fmt.Errorf("%w: %s", errUnsupportedEmbeddingProvider, providerName)
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
		)
		docPrefix := service.EmbeddingPrefixForProvider(providerName)

		deps.EmbeddingService = feedbackRecordsService
		deps.EmbeddingClient = embeddingClient
		deps.EmbeddingDocPrefix = docPrefix
		deps.EmbeddingMetrics = embeddingMetrics
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

func embeddingProviderAndModel(cfg *config.Config) (provider, model string) {
	if cfg.Embedding.Provider == "" || cfg.Embedding.Model == "" {
		return "", ""
	}

	providerCanonical := strings.ToLower(strings.TrimSpace(cfg.Embedding.Provider))
	if _, ok := supportedEmbeddingProviders[providerCanonical]; !ok {
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
