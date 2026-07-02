package service

import (
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// EmotionsProvider enqueues one emotion job per eligible feedback record event, over the shared
// enrichmentProvider. Eligibility is a text field with non-empty open text; re-classification is
// triggered by a value_text change (emotions do not depend on source language, which is only a
// prompt hint). Like sentiment it resolves a per-tenant setting on the enqueue path — the
// per-directory emotions switch (ENG-1573) — and skips tenants that have turned emotions off.
type EmotionsProvider struct {
	*enrichmentProvider
}

// NewEmotionsProvider creates a provider that enqueues feedback_emotions jobs.
// resolver reads the tenant's per-directory emotions switch; metrics may be nil when disabled.
func NewEmotionsProvider(
	inserter RiverJobInserter,
	resolver TenantSettingsReader,
	queueName string,
	maxAttempts int,
	metrics observability.EmotionsMetrics,
) *EmotionsProvider {
	return &EmotionsProvider{
		enrichmentProvider: newEnrichmentProvider(enrichmentProviderConfig{
			name:        "emotions",
			inserter:    inserter,
			resolver:    resolver,
			metrics:     metrics,
			queueName:   queueName,
			maxAttempts: maxAttempts,
			// Re-classify only when the text changes: emotions depend on value_text alone, not on
			// source language (a prompt hint only), like sentiment.
			triggers:                []string{"value_text"},
			eligible:                (*models.FeedbackRecord).IsTextField,
			hasContent:              (*models.FeedbackRecord).HasOpenText,
			gated:                   true,
			failOpenOnSettingsError: true,
			buildArgs: func(record *models.FeedbackRecord, settings *models.TenantSettings) (river.JobArgs, bool) {
				// Per-directory gate: skip tenants that switched emotion enrichment off. settings is
				// nil when the read failed and we are failing open — enqueue and let the worker
				// re-check the gate (the authoritative check before the LLM call).
				if settings != nil && !settings.Settings.EmotionsEnrichmentEnabled() {
					return nil, false
				}

				return FeedbackEmotionsArgs{
					FeedbackRecordID: record.ID,
					ValueTextHash:    emotionsContentHash(record.ValueText),
				}, true
			},
		}),
	}
}

// emotionsContentHash hashes value_text for dedupe, so an edit re-enqueues. Empty/nil value_text
// returns "empty" (a clear). Emotions do not depend on source language, so — like sentiment —
// language is not part of the hash.
func emotionsContentHash(valueText *string) string {
	return hashContent(normalizedText(valueText))
}
