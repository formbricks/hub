package service

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// EmbeddingProvider implements eventPublisher by enqueueing one River job per feedback record event
// when the event is FeedbackRecordCreated (with non-empty value_text) or FeedbackRecordUpdated
// (with value_text in ChangedFields, including when value_text is now empty so the worker can clear).
type EmbeddingProvider struct {
	inserter    FeedbackEmbeddingInserter
	apiKey      string
	queueName   string
	maxAttempts int
	metrics     observability.EmbeddingMetrics
}

// NewEmbeddingProvider creates a provider that enqueues feedback_embedding jobs.
// metrics may be nil when metrics are disabled.
func NewEmbeddingProvider(
	inserter FeedbackEmbeddingInserter,
	apiKey string,
	queueName string,
	maxAttempts int,
	metrics observability.EmbeddingMetrics,
) *EmbeddingProvider {
	return &EmbeddingProvider{
		inserter:    inserter,
		apiKey:      apiKey,
		queueName:   queueName,
		maxAttempts: maxAttempts,
		metrics:     metrics,
	}
}

// PublishEvent enqueues a feedback_embedding job when the event is FeedbackRecordCreated (with non-empty value_text)
// or FeedbackRecordUpdated (with value_text in ChangedFields). On update, the job is enqueued even when value_text
// is now empty so the worker can clear the embedding for text fields.
func (p *EmbeddingProvider) PublishEvent(ctx context.Context, event Event) {
	if p.apiKey == "" {
		return
	}

	if event.Type == datatypes.FeedbackRecordUpdated {
		if !contains(event.ChangedFields, "value_text") {
			slog.Debug("embedding: skip, value_text not in changed fields",
				"event_id", event.ID,
				"feedback_record_id", recordIDFromEventData(event.Data),
			)

			return
		}
	} else if event.Type != datatypes.FeedbackRecordCreated {
		return
	}

	record, ok := event.Data.(*models.FeedbackRecord)
	if !ok {
		slog.Debug("embedding: skip, event data is not *FeedbackRecord", "event_id", event.ID)

		return
	}

	// On create, only enqueue when there is embeddable text. On update we enqueue regardless so the worker can clear.
	if event.Type == datatypes.FeedbackRecordCreated &&
		(record.ValueText == nil || strings.TrimSpace(*record.ValueText) == "") {
		slog.Debug("embedding: skip, no value_text on create", "feedback_record_id", record.ID)

		return
	}

	opts := &river.InsertOpts{
		Queue:       p.queueName,
		MaxAttempts: p.maxAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: uniqueByPeriodEmbedding},
	}

	_, err := p.inserter.Insert(ctx, FeedbackEmbeddingArgs{FeedbackRecordID: record.ID}, opts)
	if err != nil {
		if p.metrics != nil {
			p.metrics.RecordProviderError(ctx, "enqueue_failed")
		}

		slog.Error("embedding: enqueue failed",
			"event_id", event.ID,
			"feedback_record_id", record.ID,
			"error", err,
		)

		return
	}

	slog.Info("embedding: job enqueued",
		"event_id", event.ID,
		"feedback_record_id", record.ID,
	)

	if p.metrics != nil {
		p.metrics.RecordJobsEnqueued(ctx, 1)
	}
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}

func recordIDFromEventData(data any) uuid.UUID {
	if r, ok := data.(*models.FeedbackRecord); ok {
		return r.ID
	}

	return uuid.Nil
}
