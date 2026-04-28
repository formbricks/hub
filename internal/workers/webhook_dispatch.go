package workers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// WebhookDeliveryTimeoutBuffer is added to HTTP timeout for the River job timeout.
const WebhookDeliveryTimeoutBuffer = 5 * time.Second

// WebhookDispatchWorker delivers one event to one webhook endpoint.
type WebhookDispatchWorker struct {
	river.WorkerDefaults[service.WebhookDispatchArgs]

	repo       webhookDispatchRepo
	sender     service.WebhookSender
	jobTimeout time.Duration // HTTP timeout + buffer
	metrics    observability.WebhookMetrics
}

// webhookDispatchRepo is the minimal repo interface needed by the worker.
type webhookDispatchRepo interface {
	GetByID(ctx context.Context, id uuid.UUID) (*models.Webhook, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error)
}

// NewWebhookDispatchWorker creates a worker that uses the given repo and sender.
// httpTimeout is the webhook HTTP client timeout; job timeout is httpTimeout + WebhookDeliveryTimeoutBuffer.
// metrics may be nil when metrics are disabled.
func NewWebhookDispatchWorker(
	repo webhookDispatchRepo, sender service.WebhookSender, httpTimeout time.Duration,
	metrics observability.WebhookMetrics,
) *WebhookDispatchWorker {
	return &WebhookDispatchWorker{
		repo:       repo,
		sender:     sender,
		jobTimeout: httpTimeout + WebhookDeliveryTimeoutBuffer,
		metrics:    metrics,
	}
}

// Timeout limits how long a single delivery can run (HTTP timeout + buffer).
func (w *WebhookDispatchWorker) Timeout(*river.Job[service.WebhookDispatchArgs]) time.Duration {
	return w.jobTimeout
}

// Work loads the webhook, builds the payload, and sends once.
func (w *WebhookDispatchWorker) Work(ctx context.Context, job *river.Job[service.WebhookDispatchArgs]) error {
	args := job.Args
	start := time.Now()

	webhook, err := w.repo.GetByID(ctx, args.WebhookID)
	if err != nil {
		if w.metrics != nil {
			w.metrics.RecordDispatchError(ctx, "get_webhook_failed")
			w.metrics.RecordDelivery(ctx, args.EventType, "failed_final")
			w.metrics.RecordWebhookDeliveryDuration(ctx, time.Since(start), args.EventType, "failed_final")
		}

		slog.Error("webhook dispatch: get webhook failed",
			"event_id", args.EventID,
			"webhook_id", args.WebhookID,
			"error", err,
		)

		return nil // no retry if webhook not found
	}

	if !webhook.Enabled {
		slog.Debug("webhook dispatch: webhook disabled, skipping",
			"event_id", args.EventID,
			"webhook_id", args.WebhookID,
		)

		return nil
	}

	jobTenantID := args.TenantID
	dataTenantID := service.TenantIDPointerFromEventData(args.Data)

	if jobTenantID != nil && dataTenantID != nil && *jobTenantID != *dataTenantID {
		if w.metrics != nil {
			w.metrics.RecordDispatchError(ctx, "tenant_mismatch")
		}

		slog.Error("webhook dispatch: job tenant_id conflicts with payload tenant_id, skipping delivery",
			"event_id", args.EventID,
			"webhook_id", args.WebhookID,
			"job_tenant_id", *jobTenantID,
			"payload_tenant_id", *dataTenantID,
		)

		return nil
	}

	tenantID := jobTenantID
	if tenantID == nil {
		tenantID = dataTenantID
	}

	if !service.WebhookMatchesTenant(webhook, tenantID) {
		if w.metrics != nil {
			w.metrics.RecordDispatchError(ctx, "tenant_mismatch")
		}

		var webhookTenantID any
		if webhook.TenantID != nil {
			webhookTenantID = *webhook.TenantID
		}

		var eventTenantID any
		if tenantID != nil {
			eventTenantID = *tenantID
		}

		slog.Error("webhook dispatch: tenant scope mismatch, skipping delivery",
			"event_id", args.EventID,
			"webhook_id", args.WebhookID,
			"webhook_tenant_id", webhookTenantID,
			"event_tenant_id", eventTenantID,
		)

		return nil
	}

	payload := service.NewWebhookPayload(args)

	err = w.sender.Send(ctx, webhook, payload)
	if err == nil {
		if w.metrics != nil {
			w.metrics.RecordDelivery(ctx, args.EventType, "success")
			w.metrics.RecordWebhookDeliveryDuration(ctx, time.Since(start), args.EventType, "success")
		}

		return nil
	}

	// Send failed
	isLastAttempt := job.Attempt >= job.MaxAttempts
	if isLastAttempt {
		if w.metrics != nil {
			w.metrics.RecordWebhookDisabled(ctx, "max_attempts")
			w.metrics.RecordDelivery(ctx, args.EventType, "failed_final")
			w.metrics.RecordWebhookDeliveryDuration(ctx, time.Since(start), args.EventType, "failed_final")
		}

		enabled := false
		reason := err.Error()
		now := time.Now()

		_, updateErr := w.repo.Update(ctx, webhook.ID, &models.UpdateWebhookRequest{
			Enabled:        &enabled,
			DisabledReason: &reason,
			DisabledAt:     &now,
		})
		if updateErr != nil {
			slog.Error("webhook dispatch: failed to disable webhook after max attempts",
				"webhook_id", webhook.ID,
				"event_id", args.EventID,
				"error", updateErr,
			)
		}

		slog.Error("webhook disabled after max delivery attempts",
			"webhook_id", webhook.ID,
			"event_id", args.EventID,
			"error", err,
		)

		return fmt.Errorf("webhook send (final attempt): %w", err)
	}

	if w.metrics != nil {
		w.metrics.RecordDelivery(ctx, args.EventType, "retry")
		w.metrics.RecordWebhookDeliveryDuration(ctx, time.Since(start), args.EventType, "retry")
	}

	slog.Warn("webhook delivery failed, will retry",
		"message_id", args.EventID.String(),
		"webhook_id", webhook.ID,
		"url", webhook.URL,
		"event_type", args.EventType,
		"error", err,
	)

	return fmt.Errorf("webhook send: %w", err)
}
