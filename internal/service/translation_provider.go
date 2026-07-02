package service

import (
	"strings"
	"time"

	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// uniqueByPeriodTranslation dedupes identical BACKFILL translation jobs (same record, target,
// and backfill run) within this window, so a rescued/retried backfill fan-out cannot double-
// enqueue the pages it already inserted. Event-driven jobs are deliberately not deduped (see
// enrichmentProvider.PublishEvent).
const uniqueByPeriodTranslation = 24 * time.Hour

// TranslationProvider enqueues one translation job per eligible feedback record event, over the
// shared enrichmentProvider. Eligibility is a text field with non-empty open text; re-translation
// is triggered by a value_text OR source-language change (translation output depends on both,
// unlike sentiment/embedding). The per-tenant target language (settings.target_language, falling
// back to defaultLang) is both the gate — a record with no resolvable target is skipped — and part
// of the job args.
type TranslationProvider struct {
	*enrichmentProvider
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
		enrichmentProvider: newEnrichmentProvider(enrichmentProviderConfig{
			name:        "translation",
			inserter:    inserter,
			resolver:    resolver,
			metrics:     metrics,
			queueName:   queueName,
			maxAttempts: maxAttempts,
			// Re-translate when the text or its source language changes: output depends on both.
			triggers:   []string{"value_text", "language"},
			eligible:   (*models.FeedbackRecord).IsTextField,
			hasContent: (*models.FeedbackRecord).HasOpenText,
			gated:      true,
			buildArgs: func(record *models.FeedbackRecord, settings *models.TenantSettings) (river.JobArgs, bool) {
				// Gate on a resolvable target language (resolved once, then reused in the args):
				// the tenant's own target wins, else the configured default; an empty result keeps
				// translation per-tenant opt-in (skip).
				target := resolveTargetLang(settings.Settings.TargetLanguage, defaultLang)
				if target == "" {
					return nil, false
				}

				sourceLang := ""
				if record.Language != nil {
					sourceLang = *record.Language
				}

				return FeedbackTranslationArgs{
					FeedbackRecordID: record.ID,
					TargetLang:       target,
					ValueTextHash:    translationContentHash(record.ValueText, sourceLang),
				}, true
			},
		}),
	}
}

// resolveTargetLang returns the tenant's own target language, or the configured default when the
// tenant has none. An empty result means translation is not enabled for this tenant.
func resolveTargetLang(tenantTarget, defaultLang string) string {
	if tenantTarget != "" {
		return tenantTarget
	}

	return defaultLang
}

// translationContentHash hashes value_text together with the source language for dedupe, so both an
// edit and a source-language correction re-enqueue. Empty/nil value_text returns "empty" (a clear),
// independent of source language.
func translationContentHash(valueText *string, sourceLang string) string {
	text := normalizedText(valueText)
	if text == "" {
		return "empty"
	}

	return hashContent(text + "\x00" + strings.TrimSpace(sourceLang))
}
