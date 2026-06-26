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

// uniqueByPeriodTranslation dedupes identical translation jobs (same record, target,
// and value_text) within this window, mirroring the embedding pipeline.
const uniqueByPeriodTranslation = 24 * time.Hour

// TranslationProvider implements eventPublisher by enqueueing one translation job per
// eligible feedback record event: FeedbackRecordCreated with non-empty open text, or
// FeedbackRecordUpdated whose value_text changed. A job is only enqueued when the
// record is a text field with non-empty value_text and the tenant has a target
// language configured (read via the settings resolver). The worker resolves the
// remaining work; ingestion is never blocked.
type TranslationProvider struct {
	inserter    RiverJobInserter
	resolver    TenantSettingsReader
	queueName   string
	maxAttempts int
	defaultLang string // fallback target language when a tenant has none; "" = per-tenant opt-in
	metrics     observability.TranslationMetrics
}

// NewTranslationProvider creates a provider that enqueues feedback_translation jobs.
// metrics may be nil when metrics are disabled.
func NewTranslationProvider(
	inserter RiverJobInserter,
	resolver TenantSettingsReader,
	queueName string,
	maxAttempts int,
	defaultLang string,
	metrics observability.TranslationMetrics,
) *TranslationProvider {
	return &TranslationProvider{
		inserter:    inserter,
		resolver:    resolver,
		queueName:   queueName,
		maxAttempts: maxAttempts,
		defaultLang: defaultLang,
		metrics:     metrics,
	}
}

// PublishEvent enqueues a feedback_translation job for an eligible create/update event.
// Failures are logged and swallowed so they never block ingestion.
func (p *TranslationProvider) PublishEvent(ctx context.Context, event Event) {
	if event.Type == datatypes.FeedbackRecordUpdated {
		// Re-translate when the text or its source language changes: translation
		// output depends on both (unlike embeddings, which ignore source language).
		if !contains(event.ChangedFields, "value_text") && !contains(event.ChangedFields, "language") {
			return
		}
	} else if event.Type != datatypes.FeedbackRecordCreated {
		return
	}

	record, ok := event.Data.(*models.FeedbackRecord)
	if !ok {
		slog.Debug("translation: skip, event data is not *FeedbackRecord", "event_id", event.ID)

		return
	}

	// Only text fields are translated.
	if record.FieldType != models.FieldTypeText {
		slog.Debug("translation: skip, not a text field", "feedback_record_id", record.ID)

		return
	}

	// On create, only enqueue when there is text to translate. On update, enqueue even
	// when value_text is now empty so the worker can clear a stale translation
	// (mirrors the embedding provider).
	if event.Type == datatypes.FeedbackRecordCreated &&
		(record.ValueText == nil || strings.TrimSpace(*record.ValueText) == "") {
		slog.Debug("translation: skip, no value_text on create", "feedback_record_id", record.ID)

		return
	}

	settings, err := p.resolver.GetSettings(ctx, record.TenantID)
	if err != nil {
		if p.metrics != nil {
			p.metrics.RecordProviderError(ctx, "settings_read_failed")
		}

		slog.Error("translation: resolve target language failed",
			"event_id", event.ID, "feedback_record_id", record.ID, "error", err)

		return
	}

	// Resolve the target: the tenant's own target_language wins; otherwise fall back to the
	// configured default (TRANSLATION_DEFAULT_LANGUAGE). An empty default keeps translation
	// per-tenant opt-in, so a tenant with no target is simply skipped.
	targetLang := settings.Settings.TargetLanguage
	if targetLang == "" {
		targetLang = p.defaultLang
	}

	if targetLang == "" {
		slog.Debug("translation: skip, no target language (tenant unset and no default)",
			"feedback_record_id", record.ID)

		return
	}

	opts := &river.InsertOpts{
		Queue:       p.queueName,
		MaxAttempts: p.maxAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: uniqueByPeriodTranslation},
	}

	sourceLang := ""
	if record.Language != nil {
		sourceLang = *record.Language
	}

	_, err = p.inserter.Insert(ctx, FeedbackTranslationArgs{
		FeedbackRecordID: record.ID,
		TargetLang:       targetLang,
		ValueTextHash:    translationContentHash(record.ValueText, sourceLang),
	}, opts)
	if err != nil {
		if p.metrics != nil {
			p.metrics.RecordProviderError(ctx, "enqueue_failed")
		}

		slog.Error("translation: enqueue failed",
			"event_id", event.ID, "feedback_record_id", record.ID, "error", err)

		return
	}

	slog.Info("translation: job enqueued",
		"event_id", event.ID, "feedback_record_id", record.ID, "target_lang", targetLang)

	if p.metrics != nil {
		p.metrics.RecordJobsEnqueued(ctx, 1)
	}
}

// translationContentHash hashes the inputs that determine the translation — the
// trimmed, NFC-normalized value_text and the source language — for dedupe, so a
// source-language correction re-enqueues. Empty/nil value_text returns "empty" (a
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
