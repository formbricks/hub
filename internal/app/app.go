// Package app provides application bootstrap, wiring, and lifecycle (Run, Shutdown).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/api/middleware"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/googleai"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/openai"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
)

var (
	errUnsupportedEmbeddingProvider    = errors.New("unsupported embedding provider")
	errEmbeddingProviderAPIKeyRequired = errors.New("EMBEDDING_PROVIDER_API_KEY is required for this provider")
)

const (
	webhookDeliveryBufferOverHTTP     = 5 * time.Second
	riverQueueDepthInterval           = 15 * time.Second
	defaultWebhookHTTPTimeoutFallback = 15 * time.Second
)

// App holds all server dependencies and coordinates startup and shutdown.
type App struct {
	cfg            *config.Config
	db             *pgxpool.Pool
	server         *http.Server
	river          *river.Client[pgx.Tx]
	message        *service.MessagePublisherManager
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
	metrics        *observability.Metrics
}

// setupMetrics creates meter provider and hub metrics when metrics are enabled.
func setupMetrics(cfg *config.Config) (*sdkmetric.MeterProvider, *observability.Metrics, error) {
	mp, err := observability.NewMeterProvider(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("create meter provider: %w", err)
	}

	if mp == nil {
		return nil, nil, nil
	}

	metrics, err := observability.NewMetrics(mp.Meter("hub"))
	if err != nil {
		err2 := observability.ShutdownMeterProvider(context.Background(), mp)
		if err2 != nil {
			slog.Error("shutdown meter provider after metrics error", "error", err2)
		}

		return nil, nil, fmt.Errorf("create metrics: %w", err)
	}

	return mp, metrics, nil
}

// NewApp builds and wires all components. It does not start the HTTP server or River;
// call Run to start and block until shutdown or failure.
func NewApp(cfg *config.Config, db *pgxpool.Pool) (*App, error) {
	var (
		err           error
		meterProvider *sdkmetric.MeterProvider
		metrics       *observability.Metrics
	)

	if cfg.OtelMetricsExporter == "" {
		slog.Warn("metrics not enabled (OTEL_METRICS_EXPORTER empty or unset)")
	} else {
		meterProvider, metrics, err = setupMetrics(cfg)
		if err != nil {
			return nil, err
		}
	}

	var (
		eventMetrics     observability.EventMetrics
		webhookMetrics   observability.WebhookMetrics
		embeddingMetrics observability.EmbeddingMetrics
	)
	if metrics != nil {
		eventMetrics = metrics.Events
		webhookMetrics = metrics.Webhooks
		embeddingMetrics = metrics.Embeddings
	}

	var tracerProvider *sdktrace.TracerProvider

	if cfg.OtelTracesExporter == "" {
		slog.Warn("tracing not enabled (OTEL_TRACES_EXPORTER empty or unset)")
	} else {
		tracerProvider, err = observability.NewTracerProvider(cfg)
		if err != nil {
			if meterProvider != nil {
				if err2 := observability.ShutdownMeterProvider(context.Background(), meterProvider); err2 != nil {
					slog.Error("shutdown meter provider after tracer provider error", "error", err2)
				}
			}

			return nil, fmt.Errorf("create tracer provider: %w", err)
		}
	}

	defaultHandler := slog.Default().Handler()
	slog.SetDefault(slog.New(observability.NewTraceContextHandler(defaultHandler)))

	if tracerProvider != nil {
		otel.SetTracerProvider(tracerProvider)
	}

	if meterProvider != nil {
		otel.SetMeterProvider(meterProvider)
	}

	messageManager := service.NewMessagePublisherManager(cfg.MessagePublisherBufferSize, cfg.MessagePublisherPerEventTimeout, eventMetrics)

	webhooksRepo := repository.NewDBWebhooksRepository(db)

	effectiveWebhookHTTPTimeout := cfg.WebhookHTTPTimeout
	if effectiveWebhookHTTPTimeout <= 0 {
		effectiveWebhookHTTPTimeout = defaultWebhookHTTPTimeoutFallback
	}

	webhookSender := service.NewWebhookSenderImpl(webhooksRepo, webhookMetrics, effectiveWebhookHTTPTimeout)
	webhookDeliveryTimeout := effectiveWebhookHTTPTimeout + webhookDeliveryBufferOverHTTP
	webhookWorker := workers.NewWebhookDispatchWorker(webhooksRepo, webhookSender, webhookMetrics, webhookDeliveryTimeout)
	riverWorkers := river.NewWorkers()
	river.AddWorker(riverWorkers, webhookWorker)

	queues := map[string]river.QueueConfig{
		river.QueueDefault: {MaxWorkers: cfg.WebhookDeliveryMaxConcurrent},
	}

	feedbackRecordsRepo := repository.NewDBFeedbackRecordsRepository(db)
	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	embeddingProviderName, embeddingModel := EmbeddingProviderAndModel(cfg)
	embeddingModelForDB := embeddingModel

	var embeddingDocPrefix string
	if embeddingProviderName != "" {
		embeddingDocPrefix = service.EmbeddingPrefixForProvider(embeddingProviderName)
		queues[service.EmbeddingsQueueName] = river.QueueConfig{MaxWorkers: cfg.EmbeddingMaxConcurrent}
	}

	feedbackRecordsService := service.NewFeedbackRecordsService(
		feedbackRecordsRepo,
		embeddingsRepo,
		embeddingModelForDB,
		messageManager,
		nil,
		service.EmbeddingsQueueName,
		cfg.EmbeddingMaxAttempts,
	)

	var searchHandler *handlers.SearchHandler

	if embeddingProviderName != "" {
		if (embeddingProviderName == embeddingProviderOpenAI || embeddingProviderName == embeddingProviderGoogle) &&
			cfg.EmbeddingProviderAPIKey == "" {
			return nil, fmt.Errorf("%w: %s", errEmbeddingProviderAPIKeyRequired, embeddingProviderName)
		}

		var embeddingClient service.EmbeddingClient

		switch embeddingProviderName {
		case embeddingProviderOpenAI:
			embeddingClient = openai.NewClient(cfg.EmbeddingProviderAPIKey,
				openai.WithModel(embeddingModel),
				openai.WithNormalize(cfg.EmbeddingNormalize),
			)
		case embeddingProviderGoogle:
			googleClient, err := googleai.NewClient(context.Background(), cfg.EmbeddingProviderAPIKey,
				googleai.WithModel(embeddingModel),
				googleai.WithNormalize(cfg.EmbeddingNormalize),
			)
			if err != nil {
				return nil, fmt.Errorf("create google embedding client: %w", err)
			}

			embeddingClient = googleClient
		default:
			return nil, fmt.Errorf("%w: %s", errUnsupportedEmbeddingProvider, embeddingProviderName)
		}

		embeddingWorker := workers.NewFeedbackEmbeddingWorker(
			feedbackRecordsService, embeddingClient, embeddingDocPrefix, embeddingMetrics)
		river.AddWorker(riverWorkers, embeddingWorker)

		const searchQueryCacheSize = 1000

		queryCache, err := lru.New[string, []float32](searchQueryCacheSize)
		if err != nil {
			return nil, fmt.Errorf("create search query cache: %w", err)
		}

		var cacheMetrics observability.CacheMetrics
		if metrics != nil {
			cacheMetrics = metrics.Cache
		}

		searchService := service.NewSearchService(service.SearchServiceParams{
			EmbeddingClient: embeddingClient,
			EmbeddingsRepo:  embeddingsRepo,
			Model:           embeddingModel,
			QueryCache:      queryCache,
			CacheMetrics:    cacheMetrics,
			Logger:          slog.Default(),
		})
		searchHandler = handlers.NewSearchHandler(searchService)
	} else {
		searchHandler = handlers.NewSearchHandler(nil)
	}

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues:  queues,
		Workers: riverWorkers,
	})
	if err != nil {
		messageManager.Shutdown()

		if tracerProvider != nil {
			if err2 := observability.ShutdownTracerProvider(context.Background(), tracerProvider); err2 != nil {
				slog.Error("shutdown tracer provider after River client error", "error", err2)
			}
		}

		if meterProvider != nil {
			if err2 := observability.ShutdownMeterProvider(context.Background(), meterProvider); err2 != nil {
				slog.Error("shutdown meter provider after River client error", "error", err2)
			}
		}

		return nil, fmt.Errorf("create River client: %w", err)
	}

	feedbackRecordsService.SetEmbeddingInserter(riverClient)

	webhookInserter := service.NewRetryingWebhookDispatchInserter(service.RetryingWebhookDispatchInserterConfig{
		MaxRetries:     cfg.WebhookEnqueueMaxRetries,
		InitialBackoff: cfg.WebhookEnqueueInitialBackoff,
		MaxBackoff:     cfg.WebhookEnqueueMaxBackoff,
		Metrics:        webhookMetrics,
		BaseInserter:   riverClient,
	})

	webhookProvider := service.NewWebhookProvider(
		webhookInserter, webhooksRepo,
		cfg.WebhookDeliveryMaxAttempts, cfg.WebhookMaxFanOutPerEvent,
		webhookMetrics,
	)
	messageManager.RegisterProvider(webhookProvider)

	if embeddingProviderName != "" {
		embeddingProv := service.NewEmbeddingProvider(
			riverClient,
			cfg.EmbeddingProviderAPIKey,
			embeddingModelForDB,
			service.EmbeddingsQueueName,
			cfg.EmbeddingMaxAttempts,
			embeddingDocPrefix,
			embeddingMetrics,
		)
		messageManager.RegisterProvider(embeddingProv)
	}

	webhooksService := service.NewWebhooksService(webhooksRepo, messageManager, cfg.WebhookMaxCount, cfg.WebhookURLBlacklist)
	webhooksHandler := handlers.NewWebhooksHandler(webhooksService)

	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)
	healthHandler := handlers.NewHealthHandler()

	server := newHTTPServer(
		cfg, healthHandler, feedbackRecordsHandler, webhooksHandler, searchHandler,
		meterProvider, tracerProvider,
	)

	return &App{
		cfg:            cfg,
		db:             db,
		server:         server,
		river:          riverClient,
		message:        messageManager,
		meterProvider:  meterProvider,
		tracerProvider: tracerProvider,
		metrics:        metrics,
	}, nil
}

func newHTTPServer(
	cfg *config.Config,
	health *handlers.HealthHandler,
	feedback *handlers.FeedbackRecordsHandler,
	webhooks *handlers.WebhooksHandler,
	search *handlers.SearchHandler,
	meterProvider *sdkmetric.MeterProvider,
	tracerProvider *sdktrace.TracerProvider,
) *http.Server {
	public := http.NewServeMux()
	public.HandleFunc("GET /health", health.Check)

	protected := http.NewServeMux()
	protected.HandleFunc("POST /v1/feedback-records", feedback.Create)
	protected.HandleFunc("GET /v1/feedback-records", feedback.List)
	protected.HandleFunc("GET /v1/feedback-records/{id}", feedback.Get)
	protected.HandleFunc("PATCH /v1/feedback-records/{id}", feedback.Update)
	protected.HandleFunc("DELETE /v1/feedback-records/{id}", feedback.Delete)
	protected.HandleFunc("DELETE /v1/feedback-records", feedback.BulkDelete)

	protected.HandleFunc("POST /v1/webhooks", webhooks.Create)
	protected.HandleFunc("GET /v1/webhooks", webhooks.List)
	protected.HandleFunc("GET /v1/webhooks/{id}", webhooks.Get)
	protected.HandleFunc("PATCH /v1/webhooks/{id}", webhooks.Update)
	protected.HandleFunc("DELETE /v1/webhooks/{id}", webhooks.Delete)

	protected.HandleFunc("POST /v1/feedback-records/search/semantic", search.SemanticSearch)
	protected.HandleFunc("GET /v1/feedback-records/{id}/similar", search.SimilarFeedback)

	protectedWithAuth := middleware.Auth(cfg.APIKey)(protected)
	mux := http.NewServeMux()
	mux.Handle("/v1/", protectedWithAuth)
	mux.Handle("/", public)

	otelOpts := []otelhttp.Option{
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/health"
		}),
	}
	if meterProvider != nil {
		otelOpts = append(otelOpts, otelhttp.WithMeterProvider(meterProvider))
	}

	if tracerProvider != nil {
		otelOpts = append(otelOpts, otelhttp.WithTracerProvider(tracerProvider))
	}

	inner := middleware.Logging(mux)
	handler := otelhttp.NewHandler(inner, "hub-api", otelOpts...)
	handler = middleware.RequestID(handler)

	const (
		readTimeout  = 15 * time.Second
		writeTimeout = 15 * time.Second
		idleTimeout  = 60 * time.Second
	)

	return &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}
}

// Run starts the HTTP server and River, then blocks until ctx is cancelled or a component fails.
func (a *App) Run(ctx context.Context) error {
	runErr := make(chan error, 1)

	riverCtx, cancelRiver := context.WithCancel(ctx)
	defer cancelRiver()

	if a.metrics != nil && a.metrics.Events != nil {
		go runRiverQueueDepthPoller(riverCtx, a.db, a.metrics.Events)
	}

	go func() {
		if err := a.river.Start(riverCtx); err != nil && !errors.Is(err, context.Canceled) {
			select {
			case runErr <- fmt.Errorf("river: %w", err):
			default:
			}
		}
	}()

	go func() {
		slog.Info("Starting server", "port", a.cfg.Port)

		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case runErr <- fmt.Errorf("server: %w", err):
			default:
			}
		}
	}()

	select {
	case err := <-runErr:
		cancelRiver()

		return err
	case <-ctx.Done():
		cancelRiver()

		return nil
	}
}

func runRiverQueueDepthPoller(ctx context.Context, db *pgxpool.Pool, eventMetrics observability.EventMetrics) {
	ticker := time.NewTicker(riverQueueDepthInterval)
	defer ticker.Stop()

	update := func() {
		var count int

		err := db.QueryRow(ctx,
			`SELECT COUNT(*) FROM river_job WHERE queue = $1 AND state IN ($2, $3, $4)`,
			river.QueueDefault,
			rivertype.JobStateAvailable, rivertype.JobStateRetryable, rivertype.JobStateScheduled,
		).Scan(&count)
		if err != nil {
			slog.WarnContext(ctx, "river queue depth poll failed", "error", err)

			return
		}

		eventMetrics.SetRiverQueueDepth(count)
	}

	update()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			update()
		}
	}
}

// ShutdownObservability shuts down tracer and meter providers. Logs secondary errors, returns the first.
// Exported for testing. Safe to call with nil providers.
func ShutdownObservability(ctx context.Context, tracer *sdktrace.TracerProvider, meter *sdkmetric.MeterProvider) error {
	var first error

	if tracer != nil {
		if err := observability.ShutdownTracerProvider(ctx, tracer); err != nil {
			first = err
		}
	}

	if meter != nil {
		if err := observability.ShutdownMeterProvider(ctx, meter); err != nil {
			if first == nil {
				first = err
			} else {
				slog.Error("shutdown meter provider", "error", err)
			}
		}
	}

	return first
}

// Shutdown stops the server, message publisher, and River in order. Call after Run returns.
func (a *App) Shutdown(ctx context.Context) (err error) {
	defer a.message.Shutdown()

	defer func() {
		obsErr := ShutdownObservability(ctx, a.tracerProvider, a.meterProvider)
		if err == nil {
			err = obsErr
		} else if obsErr != nil {
			slog.Error("shutdown observability", "error", obsErr)
		}
	}()

	if err = a.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		if stopErr := a.river.Stop(ctx); stopErr != nil {
			slog.Error("river stop during server shutdown", "error", stopErr)
		}

		return fmt.Errorf("server shutdown: %w", err)
	}

	if err = a.river.Stop(ctx); err != nil {
		return fmt.Errorf("river stop: %w", err)
	}

	return nil
}
