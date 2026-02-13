package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/api/middleware"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/observability"
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
	eventMetrics   observability.EventMetrics
}

const riverQueueDepthInterval = 15 * time.Second

// setupMetrics creates meter provider, handler, event metrics, and webhook metrics when metrics are enabled.
func setupMetrics(cfg *config.Config) (
	*sdkmetric.MeterProvider, http.Handler, observability.EventMetrics, observability.WebhookMetrics, error,
) {
	mp, handler, err := observability.NewMeterProvider(cfg)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("create meter provider: %w", err)
	}

	meter := mp.Meter("hub")

	eventMetrics, err := observability.NewEventMetrics(meter)
	if err != nil {
		err2 := observability.ShutdownMeterProvider(context.Background(), mp)
		if err2 != nil {
			slog.Error("shutdown meter provider after event metrics error", "error", err2)
		}

		return nil, nil, nil, nil, fmt.Errorf("create event metrics: %w", err)
	}

	webhookMetrics, err := observability.NewWebhookMetrics(meter)
	if err != nil {
		err2 := observability.ShutdownMeterProvider(context.Background(), mp)
		if err2 != nil {
			slog.Error("shutdown meter provider after webhook metrics error", "error", err2)
		}

		return nil, nil, nil, nil, fmt.Errorf("create webhook metrics: %w", err)
	}

	return mp, handler, eventMetrics, webhookMetrics, nil
}

// NewApp builds and wires all components. It does not start the HTTP server or River;
// call Run to start and block until shutdown or failure.
func NewApp(cfg *config.Config, db *pgxpool.Pool) (*App, error) {
	var (
		err            error
		meterProvider  *sdkmetric.MeterProvider
		metricsHandler http.Handler
		eventMetrics   observability.EventMetrics
		webhookMetrics observability.WebhookMetrics
	)

	if cfg.OtelMetricsExporter == "" {
		slog.Warn("metrics not enabled (OTEL_METRICS_EXPORTER empty or unset)")
	} else {
		meterProvider, metricsHandler, eventMetrics, webhookMetrics, err = setupMetrics(cfg)
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

		defaultHandler := slog.Default().Handler()
		slog.SetDefault(slog.New(observability.NewTraceContextHandler(defaultHandler)))
	}

	messageManager := service.NewMessagePublisherManager(cfg.MessagePublisherBufferSize, cfg.MessagePublisherPerEventTimeout, eventMetrics)

	webhooksRepo := repository.NewWebhooksRepository(db)
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

	webhookProvider := service.NewWebhookProvider(
		riverClient, webhooksRepo,
		cfg.WebhookDeliveryMaxAttempts, cfg.WebhookMaxFanOutPerEvent,
		webhookMetrics,
	)
	messageManager.RegisterProvider(webhookProvider)

	webhooksService := service.NewWebhooksService(webhooksRepo, messageManager, cfg.WebhookMaxCount)
	webhooksHandler := handlers.NewWebhooksHandler(webhooksService)

	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	feedbackRecordsService := service.NewFeedbackRecordsService(feedbackRecordsRepo, messageManager)
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)
	healthHandler := handlers.NewHealthHandler()

	server := newHTTPServer(cfg, healthHandler, feedbackRecordsHandler, webhooksHandler, meterProvider, tracerProvider, metricsHandler)

	return &App{
		cfg:            cfg,
		db:             db,
		server:         server,
		river:          riverClient,
		message:        messageManager,
		meterProvider:  meterProvider,
		tracerProvider: tracerProvider,
		eventMetrics:   eventMetrics,
	}, nil
}

// newHTTPServer builds the HTTP server and muxes (no auth on /health, API key on /v1/).
// When metricsHandler is non-nil, GET /metrics is registered. Handler chain: RequestID -> Logging -> otelhttp -> mux.
func newHTTPServer(
	cfg *config.Config,
	health *handlers.HealthHandler,
	feedback *handlers.FeedbackRecordsHandler,
	webhooks *handlers.WebhooksHandler,
	meterProvider *sdkmetric.MeterProvider,
	tracerProvider *sdktrace.TracerProvider,
	metricsHandler http.Handler,
) *http.Server {
	public := http.NewServeMux()
	public.HandleFunc("GET /health", health.Check)

	if metricsHandler != nil {
		public.Handle("GET /metrics", metricsHandler)
	}

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

	otelOpts := []otelhttp.Option{}
	if meterProvider != nil {
		otelOpts = append(otelOpts, otelhttp.WithMeterProvider(meterProvider))
	}

	if tracerProvider != nil {
		otelOpts = append(otelOpts, otelhttp.WithTracerProvider(tracerProvider))
	}

	handler := middleware.RequestID(middleware.Logging(otelhttp.NewHandler(mux, "hub-api", otelOpts...)))

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

	if a.eventMetrics != nil {
		go runRiverQueueDepthPoller(riverCtx, a.db, a.eventMetrics)
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

// Shutdown stops the server, message publisher, and River in order. Call after Run returns.
func (a *App) Shutdown(ctx context.Context) error {
	defer a.message.Shutdown()

	if err := a.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		if err2 := a.river.Stop(ctx); err2 != nil {
			slog.Error("river stop after server shutdown error", "error", err2)
		}

		if a.tracerProvider != nil {
			if err2 := observability.ShutdownTracerProvider(ctx, a.tracerProvider); err2 != nil {
				slog.Error("shutdown tracer provider after server shutdown error", "error", err2)
			}
		}

		if a.meterProvider != nil {
			if err2 := observability.ShutdownMeterProvider(ctx, a.meterProvider); err2 != nil {
				slog.Error("shutdown meter provider after server shutdown error", "error", err2)
			}
		}

		return fmt.Errorf("server shutdown: %w", err)
	}

	if err := a.river.Stop(ctx); err != nil {
		if a.tracerProvider != nil {
			if err2 := observability.ShutdownTracerProvider(ctx, a.tracerProvider); err2 != nil {
				slog.Error("shutdown tracer provider after river stop error", "error", err2)
			}
		}

		if a.meterProvider != nil {
			if err2 := observability.ShutdownMeterProvider(ctx, a.meterProvider); err2 != nil {
				slog.Error("shutdown meter provider after river stop error", "error", err2)
			}
		}

		return fmt.Errorf("river stop: %w", err)
	}

	if a.tracerProvider != nil {
		if err := observability.ShutdownTracerProvider(ctx, a.tracerProvider); err != nil {
			return fmt.Errorf("tracer provider shutdown: %w", err)
		}
	}

	if a.meterProvider != nil {
		if err := observability.ShutdownMeterProvider(ctx, a.meterProvider); err != nil {
			return fmt.Errorf("meter provider shutdown: %w", err)
		}
	}

	return nil
}
