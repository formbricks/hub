package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/riverqueue/river"
	"golang.org/x/text/unicode/norm"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// uniqueByPeriodSentiment dedupes identical sentiment jobs (same record + value_text) within
// this window, mirroring the embedding and translation pipelines.
const uniqueByPeriodSentiment = 24 * time.Hour

// SentimentProvider implements eventPublisher by enqueueing one sentiment job per eligible
// feedback record event: FeedbackRecordCreated with non-empty open text, or
// FeedbackRecordUpdated whose value_text changed. On update the job is enqueued even when
// value_text is now empty, so the worker can clear a stale sentiment. The worker resolves the
// remaining work; ingestion is never blocked. It is the embedding-shaped sibling of the
// translation provider: no per-tenant target, so no settings lookup on the enqueue path.
type SentimentProvider struct {
	inserter    RiverJobInserter
	queueName   string
	maxAttempts int
	metrics     observability.SentimentMetrics
}

// NewSentimentProvider creates a provider that enqueues feedback_sentiment jobs.
// metrics may be nil when metrics are disabled.
func NewSentimentProvider(
	inserter RiverJobInserter,
	queueName string,
	maxAttempts int,
	metrics observability.SentimentMetrics,
) *SentimentProvider {
	return &SentimentProvider{
		inserter:    inserter,
		queueName:   queueName,
		maxAttempts: maxAttempts,
		metrics:     metrics,
	}
}

// PublishEvent enqueues a feedback_sentiment job for an eligible create/update event.
// Failures are logged and swallowed so they never block ingestion.
func (p *SentimentProvider) PublishEvent(ctx context.Context, event Event) {
	if event.Type == datatypes.FeedbackRecordUpdated {
		// Re-classify only when the text changes: sentiment depends on value_text alone,
		// not on source language (unlike translation).
		if !contains(event.ChangedFields, "value_text") {
			return
		}
	} else if event.Type != datatypes.FeedbackRecordCreated {
		return
	}

	record, ok := event.Data.(*models.FeedbackRecord)
	if !ok {
		slog.Debug("sentiment: skip, event data is not *FeedbackRecord", "event_id", event.ID)

		return
	}

	// Only text fields are classified.
	if record.FieldType != models.FieldTypeText {
		slog.Debug("sentiment: skip, not a text field", "feedback_record_id", record.ID)

		return
	}

	// On create, only enqueue when there is text to classify. On update, enqueue even when
	// value_text is now empty so the worker can clear a stale sentiment (mirrors the embedding
	// provider).
	if event.Type == datatypes.FeedbackRecordCreated &&
		(record.ValueText == nil || strings.TrimSpace(*record.ValueText) == "") {
		slog.Debug("sentiment: skip, no value_text on create", "feedback_record_id", record.ID)

		return
	}

	opts := &river.InsertOpts{
		Queue:       p.queueName,
		MaxAttempts: p.maxAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: uniqueByPeriodSentiment},
	}

	_, err := p.inserter.Insert(ctx, FeedbackSentimentArgs{
		FeedbackRecordID: record.ID,
		ValueTextHash:    sentimentContentHash(record.ValueText),
	}, opts)
	if err != nil {
		if p.metrics != nil {
			p.metrics.RecordProviderError(ctx, "enqueue_failed")
		}

		slog.Error("sentiment: enqueue failed",
			"event_id", event.ID, "feedback_record_id", record.ID, "error", err)

		return
	}

	slog.Info("sentiment: job enqueued", "event_id", event.ID, "feedback_record_id", record.ID)

	if p.metrics != nil {
		p.metrics.RecordJobsEnqueued(ctx, 1)
	}
}

// sentimentContentHash hashes the trimmed, NFC-normalized value_text for dedupe, so an edit
// re-enqueues. Empty/nil value_text returns "empty" (a clear). Sentiment does not depend on
// source language, so — unlike translation — language is not part of the hash.
func sentimentContentHash(valueText *string) string {
	if valueText == nil {
		return "empty"
	}

	trimmed := strings.TrimSpace(*valueText)
	if trimmed == "" {
		return "empty"
	}

	sum := sha256.Sum256([]byte(norm.NFC.String(trimmed)))

	return hex.EncodeToString(sum[:])
}
