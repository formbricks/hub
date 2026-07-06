package service

import (
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

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
			name:        "sentiment",
			inserter:    inserter,
			resolver:    resolver,
			metrics:     metrics,
			queueName:   queueName,
			maxAttempts: maxAttempts,
			// Re-classify only when the text changes: sentiment depends on value_text alone, not
			// on source language (unlike translation). Mirrored by the repo eager-clear in
			// buildUpdateQuery (feedback_records_repository.go) — keep the two trigger sets in sync.
			triggers:                []string{"value_text"},
			eligible:                (*models.FeedbackRecord).IsTextField,
			hasContent:              (*models.FeedbackRecord).HasOpenText,
			gated:                   true,
			failOpenOnSettingsError: true,
			buildArgs: func(record *models.FeedbackRecord, settings *models.TenantSettings, eventID uuid.UUID) (river.JobArgs, bool) {
				// Per-directory gate: skip tenants that switched sentiment enrichment off. settings is
				// nil when the read failed and we are failing open — enqueue and let the worker
				// re-check the gate (the authoritative check before the LLM call).
				if settings != nil && !settings.Settings.SentimentEnrichmentEnabled() {
					return nil, false
				}

				return FeedbackSentimentArgs{
					FeedbackRecordID: record.ID,
					EventID:          eventID,
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
