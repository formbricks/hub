package service

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// WebhookDispatchInserter inserts webhook_dispatch jobs in batch (e.g. River client).
type WebhookDispatchInserter interface {
	InsertMany(ctx context.Context, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error)
}

// WebhookProviderRepository lists tenant-scoped webhooks eligible for event fan-out.
type WebhookProviderRepository interface {
	ListEnabledForEventTypeAndTenant(ctx context.Context, eventType string, tenantID *string) ([]models.Webhook, error)
}

// WebhookProvider implements eventPublisher by enqueueing one River job per (event, webhook).
type WebhookProvider struct {
	repo                  WebhookProviderRepository
	inserter              WebhookDispatchInserter
	maxAttempts           int
	maxFanOut             int
	enqueueMaxRetries     int
	enqueueInitialBackoff time.Duration
	enqueueMaxBackoff     time.Duration
	metrics               observability.WebhookMetrics
}

// NewWebhookProvider creates a provider that lists enabled webhooks and enqueues jobs via InsertMany.
// maxFanOut is the batch size for InsertMany (all matching webhooks are enqueued in batches of maxFanOut).
// enqueueMaxRetries, enqueueInitialBackoff, enqueueMaxBackoff configure retries when InsertMany fails (transient River/DB errors).
// metrics may be nil when metrics are disabled.
func NewWebhookProvider(
	inserter WebhookDispatchInserter, repo WebhookProviderRepository,
	maxAttempts, maxFanOut int,
	enqueueMaxRetries int, enqueueInitialBackoff, enqueueMaxBackoff time.Duration,
	metrics observability.WebhookMetrics,
) *WebhookProvider {
	return &WebhookProvider{
		repo:                  repo,
		inserter:              inserter,
		maxAttempts:           maxAttempts,
		maxFanOut:             maxFanOut,
		enqueueMaxRetries:     enqueueMaxRetries,
		enqueueInitialBackoff: enqueueInitialBackoff,
		enqueueMaxBackoff:     enqueueMaxBackoff,
		metrics:               metrics,
	}
}

// PublishEvent lists enabled webhooks for the event type and tenant, then enqueues one job per webhook.
// Webhooks are only eligible when the event payload has the same tenant_id.
func (p *WebhookProvider) PublishEvent(ctx context.Context, event Event) {
	tenantID := TenantIDPointerFromEventData(event.Data)
	if tenantID == nil {
		if p.metrics != nil {
			p.metrics.RecordProviderError(ctx, "missing_tenant_id")
		}

		slog.Warn("webhook provider: event has no tenant_id; skipping webhook fan-out",
			"event_id", event.ID,
			"event_type", event.Type,
		)

		return
	}

	tenantIDValue := *tenantID

	webhooks, err := p.repo.ListEnabledForEventTypeAndTenant(ctx, event.Type.String(), tenantID)
	if err != nil {
		if p.metrics != nil {
			p.metrics.RecordProviderError(ctx, "list_failed")
		}

		slog.Error("failed to list enabled webhooks for event type",
			"event_id", event.ID,
			"event_type", event.Type,
			"tenant_id", tenantIDValue,
			"error", err,
		)

		return
	}

	webhooks, skipped := filterWebhooksByTenant(webhooks, tenantID)
	if skipped > 0 {
		slog.Warn("webhook provider: skipped tenant-mismatched webhooks returned by repository",
			"event_id", event.ID,
			"event_type", event.Type,
			"tenant_id", tenantIDValue,
			"skipped", skipped,
		)
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
	baseArgs := p.eventToArgs(event, tenantID)

	var enqueued int64

	for start := 0; start < len(webhooks); start += p.maxFanOut {
		end := min(start+p.maxFanOut, len(webhooks))
		chunk := webhooks[start:end]

		params := make([]river.InsertManyParams, 0, len(chunk))
		for i := range chunk {
			args := baseArgs
			args.WebhookID = chunk[i].ID
			params = append(params, river.InsertManyParams{Args: args, InsertOpts: opts})
		}

		var insertErr error
		for attempt := 0; attempt <= p.enqueueMaxRetries; attempt++ {
			_, insertErr = p.inserter.InsertMany(ctx, params)
			if insertErr == nil {
				break
			}

			if attempt == p.enqueueMaxRetries {
				break
			}

			backoff := p.enqueueBackoffWithJitter(attempt)
			select {
			case <-ctx.Done():
				insertErr = ctx.Err()

				goto afterInsertRetry
			case <-time.After(backoff):
				// retry
			}
		}

	afterInsertRetry:
		if insertErr != nil {
			if p.metrics != nil {
				p.metrics.RecordProviderError(ctx, "enqueue_failed")
			}

			slog.Error("failed to enqueue webhook jobs after retries",
				"event_id", event.ID,
				"event_type", event.Type,
				"tenant_id", tenantIDValue,
				"error", insertErr,
			)

			if p.metrics != nil && enqueued > 0 {
				p.metrics.RecordJobsEnqueued(ctx, event.Type.String(), enqueued)
			}

			return
		}

		enqueued += int64(len(chunk))
	}

	if p.metrics != nil {
		p.metrics.RecordJobsEnqueued(ctx, event.Type.String(), enqueued)
	}
}

// enqueueBackoffJitterFactor: jitter is up to 50% of backoff.
const enqueueBackoffJitterFactor = 2

// enqueueBackoffWithJitter returns backoff duration for the given attempt (0-based).
func (p *WebhookProvider) enqueueBackoffWithJitter(attempt int) time.Duration {
	exp := min(p.enqueueInitialBackoff*time.Duration(1<<attempt), p.enqueueMaxBackoff)

	//nolint:gosec // G404: jitter for backoff is not security-sensitive
	jitter := time.Duration(rand.Int63n(int64(exp / enqueueBackoffJitterFactor)))

	return exp + jitter
}

func filterWebhooksByTenant(webhooks []models.Webhook, tenantID *string) ([]models.Webhook, int) {
	filtered := make([]models.Webhook, 0, len(webhooks))

	for i := range webhooks {
		if WebhookMatchesTenant(&webhooks[i], tenantID) {
			filtered = append(filtered, webhooks[i])
		}
	}

	return filtered, len(webhooks) - len(filtered)
}

// eventToArgs converts an Event to WebhookDispatchArgs (WebhookID must be set per webhook).
func (p *WebhookProvider) eventToArgs(event Event, tenantID *string) WebhookDispatchArgs {
	return WebhookDispatchArgs{
		EventID:       event.ID,
		EventType:     event.Type.String(),
		Timestamp:     event.Timestamp,
		Data:          event.Data,
		ChangedFields: event.ChangedFields,
		TenantID:      tenantID,
		WebhookID:     uuid.Nil, // set per webhook in PublishEvent
	}
}

// Ensure WebhookProvider implements eventPublisher.
var _ eventPublisher = (*WebhookProvider)(nil)
