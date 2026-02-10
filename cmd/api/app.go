// Package main provides the application lifecycle: bootstrap, run, and shutdown.
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
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
)

// App holds all server dependencies and coordinates startup and shutdown.
type App struct {
	cfg     *config.Config
	db      *pgxpool.Pool
	server  *http.Server
	river   *river.Client[pgx.Tx]
	message *service.MessagePublisherManager
	runCtx  context.Context
	cancel  context.CancelFunc
}

// NewApp builds and wires all components. It does not start the HTTP server or River;
// call Run to start and block until shutdown or failure.
func NewApp(cfg *config.Config, db *pgxpool.Pool) (*App, error) {
	ctx, cancel := context.WithCancel(context.Background())

	messageManager := service.NewMessagePublisherManager(cfg.MessagePublisherBufferSize, cfg.MessagePublisherPerEventTimeout)

	webhooksRepo := repository.NewWebhooksRepository(db)
	webhookSender := service.NewWebhookSenderImpl(webhooksRepo)
	webhookWorker := workers.NewWebhookDispatchWorker(webhooksRepo, webhookSender)
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

	webhookProvider := service.NewWebhookProvider(riverClient, webhooksRepo, cfg.WebhookDeliveryMaxAttempts, cfg.WebhookMaxFanOutPerEvent)
	messageManager.RegisterProvider(webhookProvider)

	webhooksService := service.NewWebhooksService(webhooksRepo, messageManager, cfg.WebhookMaxCount)
	webhooksHandler := handlers.NewWebhooksHandler(webhooksService)

	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	feedbackRecordsService := service.NewFeedbackRecordsService(feedbackRecordsRepo, messageManager)
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)
	healthHandler := handlers.NewHealthHandler()

	server := newHTTPServer(cfg, healthHandler, feedbackRecordsHandler, webhooksHandler)

	return &App{
		cfg:     cfg,
		db:      db,
		server:  server,
		river:   riverClient,
		message: messageManager,
		runCtx:  ctx,
		cancel:  cancel,
	}, nil
}

// newHTTPServer builds the HTTP server and muxes (no auth on /health, API key on /v1/).
func newHTTPServer(cfg *config.Config, health *handlers.HealthHandler, feedback *handlers.FeedbackRecordsHandler, webhooks *handlers.WebhooksHandler) *http.Server {
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

	return &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      middleware.Logging(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

// Run starts the HTTP server and River, then blocks until ctx is cancelled (e.g. signal)
// or a component fails. On component failure it cancels internal context and returns the error.
// Caller should then call Shutdown.
func (a *App) Run(ctx context.Context) error {
	runErr := make(chan error, 1)

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
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

// Shutdown stops the server, message publisher, and River in order. Call after Run returns.
func (a *App) Shutdown(ctx context.Context) error {
	defer a.message.Shutdown()
	if err := a.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		_ = a.river.Stop(ctx)
		return fmt.Errorf("server shutdown: %w", err)
	}
	if err := a.river.Stop(ctx); err != nil {
		return fmt.Errorf("river stop: %w", err)
	}
	return nil
}
