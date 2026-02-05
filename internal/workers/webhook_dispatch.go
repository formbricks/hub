// Package workers provides River job workers (e.g. webhook delivery).
package workers

import (
	"context"
	"log/slog"
	"time"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// WebhookDispatchWorker delivers one event to one webhook endpoint.
type WebhookDispatchWorker struct {
	river.WorkerDefaults[service.WebhookDispatchArgs]
	repo   webhookDispatchRepo
	sender service.WebhookSender
}

// webhookDispatchRepo is the minimal repo interface needed by the worker.
type webhookDispatchRepo interface {
	GetByID(ctx context.Context, id uuid.UUID) (*models.Webhook, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error)
}

// NewWebhookDispatchWorker creates a worker that uses the given repo and sender.
func NewWebhookDispatchWorker(repo webhookDispatchRepo, sender service.WebhookSender) *WebhookDispatchWorker {
	return &WebhookDispatchWorker{repo: repo, sender: sender}
}

// Timeout limits how long a single delivery can run (align with HTTP client timeout).
func (w *WebhookDispatchWorker) Timeout(*river.Job[service.WebhookDispatchArgs]) time.Duration {
	return 25 * time.Second
}

// Work loads the webhook, builds the payload, and sends once.
func (w *WebhookDispatchWorker) Work(ctx context.Context, job *river.Job[service.WebhookDispatchArgs]) error {
	args := job.Args

	webhook, err := w.repo.GetByID(ctx, args.WebhookID)
	if err != nil {
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

	payload := argsToPayload(args)
	if payload == nil {
		slog.Error("webhook dispatch: build payload failed",
			"event_id", args.EventID,
			"webhook_id", args.WebhookID,
		)
		return nil
	}

	err = w.sender.Send(ctx, webhook, payload)
	if err == nil {
		return nil
	}

	// Send failed
	isLastAttempt := job.Attempt >= job.MaxAttempts
	if isLastAttempt {
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
		return err
	}

	slog.Warn("webhook delivery failed, will retry",
		"message_id", args.EventID.String(),
		"webhook_id", webhook.ID,
		"url", webhook.URL,
		"event_type", args.EventType,
		"error", err,
	)
	return err
}

// argsToPayload builds a WebhookPayload from job args.
func argsToPayload(args service.WebhookDispatchArgs) *service.WebhookPayload {
	return &service.WebhookPayload{
		ID:            args.EventID,
		Type:          args.EventType,
		Timestamp:     time.Unix(args.Timestamp, 0),
		Data:          args.Data,
		ChangedFields: args.ChangedFields,
	}
}
