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

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/api/middleware"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
)

const (
	metricsReadHeaderTimeout = 10 * time.Second
	serverReadTimeout        = 15 * time.Second
	serverWriteTimeout       = 15 * time.Second
	serverIdleTimeout        = 60 * time.Second
	auxShutdownTimeout       = 5 * time.Second
)

// App holds all server dependencies and coordinates startup and shutdown.
type App struct {
	cfg           *config.Config
	db            *pgxpool.Pool
	server        *http.Server
	river         *river.Client[pgx.Tx]
	message       *service.MessagePublisherManager
	runCtx        context.Context //nolint:containedctx // used for coordinated shutdown
	cancel        context.CancelFunc
	metricsServer *http.Server
	meterProvider observability.MeterProviderShutdown
	metrics       observability.HubMetrics
}

// NewApp builds and wires all components. It does not start the HTTP server or River;
// call Run to start and block until shutdown or failure.
func NewApp(cfg *config.Config, db *pgxpool.Pool) (*App, error) {
	ctx, cancel := context.WithCancel(context.Background())

	var (
		metrics       observability.HubMetrics
		meterProvider observability.MeterProviderShutdown
		metricsServer *http.Server
	)

	if cfg.PrometheusEnabled {
		mp, metricsHandler, m, err := observability.NewMeterProvider(ctx, observability.MeterProviderConfig{})
		if err != nil {
			cancel()

			return nil, fmt.Errorf("create MeterProvider: %w", err)
		}

		meterProvider = mp
		metrics = m
		metricsServer = &http.Server{
			Addr:              ":" + cfg.PrometheusExporterPort,
			Handler:           metricsHandler,
			ReadHeaderTimeout: metricsReadHeaderTimeout,
		}
	}

	messageManager := service.NewMessagePublisherManager(cfg.MessagePublisherBufferSize, cfg.MessagePublisherPerEventTimeout, metrics)

	webhooksRepo := repository.NewWebhooksRepository(db)
	webhookSender := service.NewWebhookSenderImpl(webhooksRepo, metrics)
	webhookWorker := workers.NewWebhookDispatchWorker(webhooksRepo, webhookSender, metrics)
	riverWorkers := river.NewWorkers()
	river.AddWorker(riverWorkers, webhookWorker)

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: cfg.WebhookDeliveryMaxConcurrent},
		},
		Workers: riverWorkers,
	})
	if err != nil {
		cancel()
		messageManager.Shutdown()

		return nil, fmt.Errorf("create River client: %w", err)
	}

	webhookProvider := service.NewWebhookProvider(riverClient, webhooksRepo, cfg.WebhookDeliveryMaxAttempts, cfg.WebhookMaxFanOutPerEvent, metrics)
	messageManager.RegisterProvider(webhookProvider)

	webhooksService := service.NewWebhooksService(webhooksRepo, messageManager, cfg.WebhookMaxCount)
	webhooksHandler := handlers.NewWebhooksHandler(webhooksService)

	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	feedbackRecordsService := service.NewFeedbackRecordsService(feedbackRecordsRepo, messageManager)
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)
	healthHandler := handlers.NewHealthHandler()

	server := newHTTPServer(cfg, healthHandler, feedbackRecordsHandler, webhooksHandler, metrics)

	return &App{
		cfg:           cfg,
		db:            db,
		server:        server,
		river:         riverClient,
		message:       messageManager,
		runCtx:        ctx,
		cancel:        cancel,
		metricsServer: metricsServer,
		meterProvider: meterProvider,
		metrics:       metrics,
	}, nil
}

// newHTTPServer builds the HTTP server and muxes (no auth on /health, API key on /v1/).
func newHTTPServer(cfg *config.Config, health *handlers.HealthHandler, feedback *handlers.FeedbackRecordsHandler,
	webhooks *handlers.WebhooksHandler, metrics observability.HubMetrics,
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

	handler := middleware.Logging(mux)
	if metrics != nil {
		handler = middleware.Metrics(metrics)(handler)
	}

	return &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
		IdleTimeout:  serverIdleTimeout,
	}
}

// Run starts the HTTP server, optional metrics server, and River, then blocks until ctx is cancelled (e.g. signal)
// or a component fails. On component failure it cancels internal context and returns the error.
// Caller should then call Shutdown.
func (a *App) Run(ctx context.Context) error {
	runErr := make(chan error, 1)

	if a.metricsServer != nil {
		go func() {
			slog.Info("Starting metrics server", "port", a.cfg.PrometheusExporterPort)

			if err := a.metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				select {
				case runErr <- fmt.Errorf("metrics server: %w", err):
				default:
				}
			}
		}()
	}

	go func() {
		if err := a.river.Start(a.runCtx); err != nil && !errors.Is(err, context.Canceled) {
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
		a.cancel()

		return err
	case <-ctx.Done():
		return nil
	}
}

// Shutdown stops the server, optional metrics server, MeterProvider, message publisher, and River in order. Call after Run returns.
func (a *App) Shutdown(ctx context.Context) error {
	defer a.message.Shutdown()

	if a.metricsServer != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, auxShutdownTimeout)
		if err := a.metricsServer.Shutdown(shutdownCtx); err != nil {
			slog.Warn("Metrics server shutdown", "error", err)
		}

		cancel()
	}

	if a.meterProvider != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, auxShutdownTimeout)
		if err := a.meterProvider.Shutdown(shutdownCtx); err != nil {
			slog.Warn("MeterProvider shutdown", "error", err)
		}

		cancel()
	}

	if err := a.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		_ = a.river.Stop(ctx)

		return fmt.Errorf("server shutdown: %w", err)
	}

	if err := a.river.Stop(ctx); err != nil {
		return fmt.Errorf("river stop: %w", err)
	}

	return nil
}
