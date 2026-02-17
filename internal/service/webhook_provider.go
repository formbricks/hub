package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/formbricks/hub/internal/observability"
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
	metrics     observability.WebhookMetrics
}

// NewWebhookProvider creates a provider that lists enabled webhooks and enqueues jobs via InsertMany.
// maxFanOut is the batch size for InsertMany (all matching webhooks are enqueued in batches of maxFanOut).
// metrics may be nil when metrics are disabled.
func NewWebhookProvider(
	inserter WebhookDispatchInserter, repo WebhooksRepository, maxAttempts, maxFanOut int,
	metrics observability.WebhookMetrics,
) *WebhookProvider {
	return &WebhookProvider{
		repo:        repo,
		inserter:    inserter,
		maxAttempts: maxAttempts,
		maxFanOut:   maxFanOut,
		metrics:     metrics,
	}
}

// PublishEvent lists all enabled webhooks for the event type and enqueues one job per webhook,
// in batches of maxFanOut to avoid oversized InsertMany calls.
func (p *WebhookProvider) PublishEvent(ctx context.Context, event Event) {
	webhooks, err := p.repo.ListEnabledForEventType(ctx, event.Type.String())
	if err != nil {
		if p.metrics != nil {
			p.metrics.RecordProviderError(ctx, "list_failed")
		}

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

	const uniqueByPeriodHours = 24

	opts := &river.InsertOpts{
		MaxAttempts: p.maxAttempts,
		UniqueOpts: river.UniqueOpts{
			ByArgs:   true,
			ByPeriod: uniqueByPeriodHours * time.Hour,
		},
	}
	baseArgs := p.eventToArgs(event)

	for start := 0; start < len(webhooks); start += p.maxFanOut {
		end := min(start+p.maxFanOut, len(webhooks))
		chunk := webhooks[start:end]

		params := make([]river.InsertManyParams, 0, len(chunk))
		for i := range chunk {
			args := baseArgs
			args.WebhookID = chunk[i].ID
			params = append(params, river.InsertManyParams{Args: args, InsertOpts: opts})
		}

		_, err = p.inserter.InsertMany(ctx, params)
		if err != nil {
			if p.metrics != nil {
				p.metrics.RecordProviderError(ctx, "enqueue_failed")
			}

			slog.Error("failed to enqueue webhook jobs",
				"event_id", event.ID,
				"event_type", event.Type,
				"error", err,
			)

			return
		}
	}

	if p.metrics != nil {
		p.metrics.RecordJobsEnqueued(ctx, event.Type.String(), int64(len(webhooks)))
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
