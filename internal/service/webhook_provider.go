package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// WebhookDispatchInserter inserts webhook_dispatch jobs in batch (e.g. River client).
type WebhookDispatchInserter interface {
	InsertMany(ctx context.Context, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error)
}

// WebhookProvider implements eventPublisher by enqueueing one River job per (event, webhook).
type WebhookProvider struct {
	repo        WebhooksRepository
	inserter    WebhookDispatchInserter
	maxAttempts int
	maxFanOut   int
}

// NewWebhookProvider creates a provider that lists enabled webhooks and enqueues jobs via InsertMany (capped by maxFanOut per event).
func NewWebhookProvider(inserter WebhookDispatchInserter, repo WebhooksRepository, maxAttempts, maxFanOut int) *WebhookProvider {
	return &WebhookProvider{
		repo:        repo,
		inserter:    inserter,
		maxAttempts: maxAttempts,
		maxFanOut:   maxFanOut,
	}
}

// PublishEvent lists enabled webhooks for the event type and enqueues jobs via InsertMany (capped by maxFanOut).
func (p *WebhookProvider) PublishEvent(ctx context.Context, event Event) {
	webhooks, err := p.repo.ListEnabledForEventType(ctx, event.Type.String())
	if err != nil {
		slog.Error("failed to list enabled webhooks for event type",
			"event_id", event.ID,
			"event_type", event.Type,
			"error", err,
		)
		return
	}

	if len(webhooks) == 0 {
		return
	}

	// Cap fan-out per event to avoid blocking the publisher.
	if len(webhooks) > p.maxFanOut {
		slog.Warn("webhook fan-out capped per event",
			"event_id", event.ID,
			"event_type", event.Type,
			"requested", len(webhooks),
			"capped", p.maxFanOut,
		)
		webhooks = webhooks[:p.maxFanOut]
	}

	opts := &river.InsertOpts{
		MaxAttempts: p.maxAttempts,
		UniqueOpts: river.UniqueOpts{
			ByArgs:   true,
			ByPeriod: 24 * time.Hour,
		},
	}
	params := make([]river.InsertManyParams, 0, len(webhooks))
	baseArgs := p.eventToArgs(event)
	for i := range webhooks {
		args := baseArgs
		args.WebhookID = webhooks[i].ID
		params = append(params, river.InsertManyParams{Args: args, InsertOpts: opts})
	}

	_, err = p.inserter.InsertMany(ctx, params)
	if err != nil {
		slog.Error("failed to enqueue webhook jobs",
			"event_id", event.ID,
			"event_type", event.Type,
			"error", err,
		)
	}
}

// eventToArgs converts an Event to WebhookDispatchArgs (WebhookID must be set per webhook).
func (p *WebhookProvider) eventToArgs(event Event) WebhookDispatchArgs {
	return WebhookDispatchArgs{
		EventID:       event.ID,
		EventType:     event.Type.String(),
		Timestamp:     event.Timestamp,
		Data:          event.Data,
		ChangedFields: event.ChangedFields,
		WebhookID:     uuid.Nil, // set per webhook in PublishEvent
	}
}

// Ensure WebhookProvider implements eventPublisher.
var _ eventPublisher = (*WebhookProvider)(nil)
