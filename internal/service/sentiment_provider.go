package service

import (
	"time"

	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// uniqueByPeriodSentiment dedupes identical sentiment jobs (same record + value_text) within
// this window, mirroring the embedding and translation pipelines.
const uniqueByPeriodSentiment = 24 * time.Hour

// SentimentProvider enqueues one sentiment job per eligible feedback record event, over the shared
// enrichmentProvider. Eligibility is a text field with non-empty open text; re-classification is
// triggered by a value_text change (sentiment does not depend on source language, unlike
// translation). Like translation it resolves a per-tenant setting on the enqueue path — the
// per-directory sentiment switch (ENG-1529) — and skips tenants that have turned sentiment off.
type SentimentProvider struct {
	*enrichmentProvider
}

// NewSentimentProvider creates a provider that enqueues feedback_sentiment jobs.
// resolver reads the tenant's per-directory sentiment switch; metrics may be nil when disabled.
func NewSentimentProvider(
	inserter RiverJobInserter,
	resolver TenantSettingsReader,
	queueName string,
	maxAttempts int,
	metrics observability.SentimentMetrics,
) *SentimentProvider {
	return &SentimentProvider{
		enrichmentProvider: newEnrichmentProvider(enrichmentProviderConfig{
			name:           "sentiment",
			inserter:       inserter,
			resolver:       resolver,
			metrics:        metrics,
			queueName:      queueName,
			maxAttempts:    maxAttempts,
			uniqueByPeriod: uniqueByPeriodSentiment,
			// Re-classify only when the text changes: sentiment depends on value_text alone, not
			// on source language (unlike translation).
			triggers:   []string{"value_text"},
			eligible:   (*models.FeedbackRecord).IsTextField,
			hasContent: (*models.FeedbackRecord).HasOpenText,
			gated:      true,
			buildArgs: func(record *models.FeedbackRecord, settings *models.TenantSettings) (river.JobArgs, bool) {
				// Per-directory gate: skip tenants that switched sentiment enrichment off.
				if !settings.Settings.SentimentEnrichmentEnabled() {
					return nil, false
				}

				return FeedbackSentimentArgs{
					FeedbackRecordID: record.ID,
					ValueTextHash:    sentimentContentHash(record.ValueText),
				}, true
			},
		}),
	}
}

// sentimentContentHash hashes value_text for dedupe, so an edit re-enqueues. Empty/nil value_text
// returns "empty" (a clear). Sentiment does not depend on source language, so — unlike translation
// — language is not part of the hash.
func sentimentContentHash(valueText *string) string {
	return hashContent(normalizedText(valueText))
}
