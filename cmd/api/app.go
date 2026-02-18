package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
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
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
	"github.com/formbricks/hub/pkg/cache"
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

const riverQueueDepthInterval = 15 * time.Second

// setupMetrics creates the meter provider and all hub metrics when metrics are enabled.
// Returns (nil, nil, nil) when metrics are disabled (unsupported or disabled exporter).
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

	var eventMetrics observability.EventMetrics
	if metrics != nil {
		eventMetrics = metrics.Events
	}

	messageManager := service.NewMessagePublisherManager(cfg.MessagePublisherBufferSize, cfg.MessagePublisherPerEventTimeout, eventMetrics)

	webhooksRepoBase := repository.NewDBWebhooksRepository(db)

	webhookCacheSize := cfg.WebhookCacheSize

	webhookListCache, err := cache.NewLoaderCache[string, []models.Webhook](webhookCacheSize, func(s string) string { return s })
	if err != nil {
		return nil, fmt.Errorf("create webhook list cache: %w", err)
	}

	webhookGetByIDCache, err := cache.NewLoaderCache[uuid.UUID, *models.Webhook](
		webhookCacheSize,
		func(id uuid.UUID) string { return id.String() },
	)
	if err != nil {
		return nil, fmt.Errorf("create webhook getbyid cache: %w", err)
	}

	var (
		webhookMetrics observability.WebhookMetrics
		cacheMetrics   observability.CacheMetrics
	)

	if metrics != nil {
		webhookMetrics = metrics.Webhooks
		cacheMetrics = metrics.Cache
	}

	webhooksRepo := service.NewCachingWebhooksRepository(webhooksRepoBase, webhookListCache, webhookGetByIDCache, cacheMetrics)
	webhookSender := service.NewWebhookSenderImpl(webhooksRepo, webhookMetrics)
	webhookWorker := workers.NewWebhookDispatchWorker(webhooksRepo, webhookSender, webhookMetrics)
	riverWorkers := river.NewWorkers()
	river.AddWorker(riverWorkers, webhookWorker)

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: cfg.WebhookDeliveryMaxConcurrent},
		},
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

	webhookInserter := service.NewRetryingWebhookDispatchInserter(riverClient, service.RetryingWebhookDispatchInserterConfig{
		MaxRetries:     cfg.WebhookEnqueueMaxRetries,
		InitialBackoff: cfg.WebhookEnqueueInitialBackoff,
		MaxBackoff:     cfg.WebhookEnqueueMaxBackoff,
		Metrics:        webhookMetrics,
	})
	webhookProvider := service.NewWebhookProvider(
		webhookInserter, webhooksRepo,
		cfg.WebhookDeliveryMaxAttempts, cfg.WebhookMaxFanOutPerEvent,
		webhookMetrics,
	)
	messageManager.RegisterProvider(webhookProvider)

	webhooksService := service.NewWebhooksService(webhooksRepo, messageManager, cfg.WebhookMaxCount)
	webhooksHandler := handlers.NewWebhooksHandler(webhooksService)

	feedbackRecordsRepo := repository.NewDBFeedbackRecordsRepository(db)
	feedbackRecordsService := service.NewFeedbackRecordsService(feedbackRecordsRepo, messageManager)
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)
	healthHandler := handlers.NewHealthHandler()

	server := newHTTPServer(cfg, healthHandler, feedbackRecordsHandler, webhooksHandler, meterProvider, tracerProvider, metrics)

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
// Handler chain: RequestID -> MaxBody -> otelhttp(Logging(mux)) so access logs get trace_id/span_id from context.
func newHTTPServer(
	cfg *config.Config,
	health *handlers.HealthHandler,
	feedback *handlers.FeedbackRecordsHandler,
	webhooks *handlers.WebhooksHandler,
	meterProvider *sdkmetric.MeterProvider,
	tracerProvider *sdktrace.TracerProvider,
	metrics *observability.Metrics,
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

	var apiMetrics observability.APIMetrics
	if metrics != nil {
		apiMetrics = metrics.API
	}

	handler = middleware.MaxBody(cfg.MaxRequestBodyBytes, apiMetrics)(handler)

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

	if a.metrics != nil {
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
		_ = a.river.Stop(ctx)

		return fmt.Errorf("server shutdown: %w", err)
	}

	if err = a.river.Stop(ctx); err != nil {
		return fmt.Errorf("river stop: %w", err)
	}

	return nil
}
