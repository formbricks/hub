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

// uniqueByPeriodTranslation dedupes identical translation jobs (same record, target, and
// value_text) within this window, mirroring the embedding pipeline.
const uniqueByPeriodTranslation = 24 * time.Hour

// TranslationProvider enqueues one translation job per eligible feedback record event, over the
// shared EnrichmentProvider. Eligibility is a text field with non-empty open text; re-translation
// is triggered by a value_text OR source-language change (translation output depends on both,
// unlike sentiment/embedding). The per-tenant target language (settings.target_language, falling
// back to defaultLang) is both the gate — a record with no resolvable target is skipped — and part
// of the job args.
type TranslationProvider struct {
	*EnrichmentProvider
}

// NewTranslationProvider creates a provider that enqueues feedback_translation jobs. defaultLang is
// the fallback target when a tenant has none ("" keeps translation per-tenant opt-in). metrics may
// be nil when metrics are disabled.
func NewTranslationProvider(
	inserter RiverJobInserter,
	resolver TenantSettingsReader,
	queueName string,
	maxAttempts int,
	defaultLang string,
	metrics observability.TranslationMetrics,
) *TranslationProvider {
	return &TranslationProvider{
		EnrichmentProvider: NewEnrichmentProvider(enrichmentProviderConfig{
			name:           "translation",
			inserter:       inserter,
			resolver:       resolver,
			metrics:        metrics,
			queueName:      queueName,
			maxAttempts:    maxAttempts,
			uniqueByPeriod: uniqueByPeriodTranslation,
			// Re-translate when the text or its source language changes: output depends on both.
			triggers:   []string{"value_text", "language"},
			eligible:   translationEligible,
			hasContent: translationHasContent,
			// Gate on a resolvable target language: the tenant's own target wins, else the
			// configured default; an empty result keeps translation per-tenant opt-in (skip).
			enabled: func(settings models.EnrichmentSettings) bool {
				return resolveTargetLang(settings.TargetLanguage, defaultLang) != ""
			},
			contentHash: translationContentHashFromRecord,
			newArgs: func(record *models.FeedbackRecord, hash string, settings *models.TenantSettings) river.JobArgs {
				return FeedbackTranslationArgs{
					FeedbackRecordID: record.ID,
					TargetLang:       resolveTargetLang(settings.Settings.TargetLanguage, defaultLang),
					ValueTextHash:    hash,
				}
			},
		}),
	}
}

func translationEligible(record *models.FeedbackRecord) bool {
	return record.FieldType == models.FieldTypeText
}

func translationHasContent(record *models.FeedbackRecord) bool {
	return record.ValueText != nil && strings.TrimSpace(*record.ValueText) != ""
}

// resolveTargetLang returns the tenant's own target language, or the configured default when the
// tenant has none. An empty result means translation is not enabled for this tenant.
func resolveTargetLang(tenantTarget, defaultLang string) string {
	if tenantTarget != "" {
		return tenantTarget
	}

	return defaultLang
}

// translationContentHashFromRecord hashes the inputs that determine the translation — value_text
// and the record's source language — so a source-language correction re-enqueues.
func translationContentHashFromRecord(record *models.FeedbackRecord) string {
	sourceLang := ""
	if record.Language != nil {
		sourceLang = *record.Language
	}

	return translationContentHash(record.ValueText, sourceLang)
}

// translationContentHash hashes the trimmed, NFC-normalized value_text and the source language for
// dedupe, so a source-language correction re-enqueues. Empty/nil value_text returns "empty" (a
// clear), independent of source language.
func translationContentHash(valueText *string, sourceLang string) string {
	if valueText == nil {
		return "empty"
	}

	trimmed := strings.TrimSpace(*valueText)
	if trimmed == "" {
		return "empty"
	}

	sum := sha256.Sum256([]byte(norm.NFC.String(trimmed) + "\x00" + strings.TrimSpace(sourceLang)))

	return hex.EncodeToString(sum[:])
}
