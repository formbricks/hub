package main

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

var errUnsupportedEmbeddingProvider = errors.New("unsupported embedding provider")

const (
	embeddingProviderOpenAI = "openai"
	embeddingProviderGoogle = "google"
)

var supportedEmbeddingProviders = map[string]struct{}{
	embeddingProviderOpenAI: {},
	embeddingProviderGoogle: {},
}

const riverQueueDepthInterval = 15 * time.Second

// embeddingProviderAndModel returns (provider, model) when embeddings are enabled: EMBEDDING_PROVIDER
// is set and supported. Model and API key are optional (e.g. local provider may not need them).
// Otherwise returns ("", "") so no embedding provider or jobs run. Embeddings are optional; no defaults.
func embeddingProviderAndModel(cfg *config.Config) (provider, model string) {
	if cfg.EmbeddingProvider == "" {
		return "", ""
	}

	if _, ok := supportedEmbeddingProviders[cfg.EmbeddingProvider]; !ok {
		slog.Info("embeddings disabled: unsupported EMBEDDING_PROVIDER",
			"provider", cfg.EmbeddingProvider, "model", cfg.EmbeddingModel)

		return "", ""
	}

	return cfg.EmbeddingProvider, cfg.EmbeddingModel
}

// setupMetrics creates meter provider and hub metrics when metrics are enabled.
// When NewMeterProvider returns nil (unsupported or disabled exporter), returns (nil, nil, nil) (metrics disabled).
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

	// Install TraceContextHandler unconditionally so request_id (and trace_id/span_id when tracing is on) appear in logs.
	defaultHandler := slog.Default().Handler()
	slog.SetDefault(slog.New(observability.NewTraceContextHandler(defaultHandler)))

	if tracerProvider != nil {
		otel.SetTracerProvider(tracerProvider)
	}

	if meterProvider != nil {
		otel.SetMeterProvider(meterProvider)
	}

	messageManager := service.NewMessagePublisherManager(cfg.MessagePublisherBufferSize, cfg.MessagePublisherPerEventTimeout, eventMetrics)

	webhooksRepo := repository.NewWebhooksRepository(db)
	webhookSender := service.NewWebhookSenderImpl(webhooksRepo, webhookMetrics)
	webhookWorker := workers.NewWebhookDispatchWorker(webhooksRepo, webhookSender, webhookMetrics)
	riverWorkers := river.NewWorkers()
	river.AddWorker(riverWorkers, webhookWorker)

	queues := map[string]river.QueueConfig{
		river.QueueDefault:          {MaxWorkers: cfg.WebhookDeliveryMaxConcurrent},
		service.EmbeddingsQueueName: {MaxWorkers: cfg.EmbeddingMaxConcurrent},
	}

	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	embeddingProviderName, embeddingModel := embeddingProviderAndModel(cfg)
	// Model for DB/jobs: required for embeddings.model column; use "default" when provider has no model name (e.g. local).
	embeddingModelForDB := embeddingModel
	if embeddingModelForDB == "" {
		embeddingModelForDB = "default"
	}

	feedbackRecordsService := service.NewFeedbackRecordsService(
		feedbackRecordsRepo,
		embeddingsRepo,
		embeddingModelForDB,
		messageManager,
		nil, // riverClient set below after creation
		service.EmbeddingsQueueName,
		cfg.EmbeddingMaxAttempts,
	)

	var searchHandler *handlers.SearchHandler

	if embeddingProviderName != "" {
		var embeddingClient service.EmbeddingClient

		switch embeddingProviderName {
		case embeddingProviderOpenAI:
			embeddingClient = openai.NewClient(cfg.EmbeddingProviderAPIKey,
				openai.WithModel(embeddingModel),
			)
		case embeddingProviderGoogle:
			googleClient, err := googleai.NewClient(context.Background(), cfg.EmbeddingProviderAPIKey,
				googleai.WithModel(embeddingModel),
			)
			if err != nil {
				return nil, fmt.Errorf("create google embedding client: %w", err)
			}

			embeddingClient = googleClient
		default:
			return nil, fmt.Errorf("%w: %s", errUnsupportedEmbeddingProvider, embeddingProviderName)
		}

		embeddingWorker := workers.NewFeedbackEmbeddingWorker(feedbackRecordsService, embeddingClient, embeddingMetrics)
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
			MinScore:        cfg.SearchScoreThreshold,
			QueryCache:      queryCache,
			CacheMetrics:    cacheMetrics,
			Logger:          slog.Default(),
		})
		searchHandler = handlers.NewSearchHandler(searchService)
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

	// Enable backfill on the same service instance the embedding worker uses (avoids nil inserter if worker ever calls BackfillEmbeddings).
	feedbackRecordsService.SetEmbeddingInserter(riverClient)

	webhookProvider := service.NewWebhookProvider(
		riverClient, webhooksRepo,
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
			embeddingMetrics,
		)
		messageManager.RegisterProvider(embeddingProv)
	}

	webhooksService := service.NewWebhooksService(webhooksRepo, messageManager, cfg.WebhookMaxCount)
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

// newHTTPServer builds the HTTP server and muxes (no auth on /health, API key on /v1/).
// Handler chain: RequestID -> otelhttp(Logging(mux)) so access logs get trace_id/span_id from context.
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

	// Search is nil when no embeddings API is configured (e.g. EMBEDDING_PROVIDER unset);
	// semantic search and similar-feedback are not registered then.
	if search != nil {
		protected.HandleFunc("POST /v1/feedback-records/search/semantic", search.SemanticSearch)
		protected.HandleFunc("GET /v1/feedback-records/{id}/similar", search.SimilarFeedback)
	}

	protectedWithAuth := middleware.Auth(cfg.APIKey)(protected)
	mux := http.NewServeMux()
	mux.Handle("/v1/", protectedWithAuth)
	mux.Handle("/", public)

	otelOpts := []otelhttp.Option{
		// Skip tracing and HTTP metrics for health checks to reduce noise.
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

	// Logging runs inside otelhttp so r.Context() has the span when we log (trace_id/span_id in access logs).
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

// Run starts the HTTP server and River, then blocks until ctx is cancelled (e.g. signal)
// or a component fails. When ctx is cancelled or a component fails, it cancels the internal
// River context so River and the queue depth poller stop before Run returns. Caller should then call Shutdown.
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

// runRiverQueueDepthPoller periodically updates the River default-queue depth gauge.
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

// shutdownObservability shuts down tracer and meter providers. Logs secondary errors, returns the first.
func shutdownObservability(ctx context.Context, tracer *sdktrace.TracerProvider, meter *sdkmetric.MeterProvider) error {
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
// Observability is shut down once via defer; its error is returned only when server and River shut down successfully.
func (a *App) Shutdown(ctx context.Context) (err error) {
	defer a.message.Shutdown()

	defer func() {
		obsErr := shutdownObservability(ctx, a.tracerProvider, a.meterProvider)
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
