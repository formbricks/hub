package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/riverqueue/river"
	"golang.org/x/text/unicode/norm"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// uniqueByPeriodSentiment dedupes identical sentiment jobs (same record + value_text) within
// this window, mirroring the embedding and translation pipelines.
const uniqueByPeriodSentiment = 24 * time.Hour

// SentimentProvider enqueues one sentiment job per eligible feedback record event, over the shared
// EnrichmentProvider. Eligibility is a text field with non-empty open text; re-classification is
// triggered by a value_text change (sentiment does not depend on source language, unlike
// translation). Like translation it resolves a per-tenant setting on the enqueue path — the
// per-directory sentiment switch (ENG-1529) — and skips tenants that have turned sentiment off.
type SentimentProvider struct {
	*EnrichmentProvider
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
		EnrichmentProvider: NewEnrichmentProvider(enrichmentProviderConfig{
			name:           "sentiment",
			inserter:       inserter,
			resolver:       resolver,
			metrics:        metrics,
			queueName:      queueName,
			maxAttempts:    maxAttempts,
			uniqueByPeriod: uniqueByPeriodSentiment,
			// Re-classify only when the text changes: sentiment depends on value_text alone, not
			// on source language (unlike translation).
			triggers:    []string{"value_text"},
			eligible:    sentimentEligible,
			hasContent:  sentimentHasContent,
			enabled:     sentimentEnabled,
			contentHash: func(r *models.FeedbackRecord) string { return sentimentContentHash(r.ValueText) },
			newArgs:     newSentimentArgs,
		}),
	}
}

// sentimentEligible reports whether a record can be classified: only text fields carry open text.
func sentimentEligible(record *models.FeedbackRecord) bool {
	return record.FieldType == models.FieldTypeText
}

// sentimentHasContent reports whether a record has non-empty open text to classify (create gate).
func sentimentHasContent(record *models.FeedbackRecord) bool {
	return record.ValueText != nil && strings.TrimSpace(*record.ValueText) != ""
}

// sentimentEnabled reports the tenant's per-directory sentiment switch (default-on, opt-out).
func sentimentEnabled(settings models.EnrichmentSettings) bool {
	return settings.SentimentEnrichmentEnabled()
}

// newSentimentArgs builds the job payload; uniqueness is by (record, value_text hash). Sentiment
// derives nothing from tenant settings, so the settings argument is unused.
func newSentimentArgs(record *models.FeedbackRecord, hash string, _ *models.TenantSettings) river.JobArgs {
	return FeedbackSentimentArgs{FeedbackRecordID: record.ID, ValueTextHash: hash}
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
