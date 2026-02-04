package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// WebhookDispatchInserter inserts webhook_dispatch jobs (e.g. River client).
type WebhookDispatchInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// WebhookProvider implements eventPublisher by enqueueing one River job per (event, webhook).
type WebhookProvider struct {
	repo        WebhooksRepository
	inserter    WebhookDispatchInserter
	maxAttempts int
}

// NewWebhookProvider creates a provider that lists enabled webhooks and enqueues one job per webhook.
func NewWebhookProvider(inserter WebhookDispatchInserter, repo WebhooksRepository, maxAttempts int) *WebhookProvider {
	return &WebhookProvider{
		repo:        repo,
		inserter:    inserter,
		maxAttempts: maxAttempts,
	}
}

// PublishEvent lists enabled webhooks for the event type and inserts one job per webhook.
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

	args := p.eventToArgs(event)
	opts := &river.InsertOpts{
		MaxAttempts: p.maxAttempts,
		UniqueOpts: river.UniqueOpts{
			ByArgs:   true,
			ByPeriod: 24 * time.Hour,
		},
	}

	for _, webhook := range webhooks {
		args.WebhookID = webhook.ID
		_, err := p.inserter.Insert(ctx, args, opts)
		if err != nil {
			slog.Error("failed to enqueue webhook job",
				"event_id", event.ID,
				"webhook_id", webhook.ID,
				"error", err,
			)
		}
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
