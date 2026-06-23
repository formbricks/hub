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
}

// NewTranslationProvider creates a provider that enqueues feedback_translation jobs.
func NewTranslationProvider(
	inserter RiverJobInserter,
	resolver TenantSettingsReader,
	queueName string,
	maxAttempts int,
) *TranslationProvider {
	return &TranslationProvider{
		inserter:    inserter,
		resolver:    resolver,
		queueName:   queueName,
		maxAttempts: maxAttempts,
	}
}

// PublishEvent enqueues a feedback_translation job for an eligible create/update event.
// Failures are logged and swallowed so they never block ingestion.
func (p *TranslationProvider) PublishEvent(ctx context.Context, event Event) {
	if event.Type == datatypes.FeedbackRecordUpdated {
		if !contains(event.ChangedFields, "value_text") {
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

	// Only open-text answers are translated.
	if record.FieldType != models.FieldTypeText || record.ValueText == nil ||
		strings.TrimSpace(*record.ValueText) == "" {
		slog.Debug("translation: skip, not an eligible non-empty text record", "feedback_record_id", record.ID)

		return
	}

	settings, err := p.resolver.GetSettings(ctx, record.TenantID)
	if err != nil {
		slog.Error("translation: resolve target language failed",
			"event_id", event.ID, "feedback_record_id", record.ID, "error", err)

		return
	}

	targetLang := settings.Settings.TargetLanguage
	if targetLang == "" {
		slog.Debug("translation: skip, tenant has no target language", "feedback_record_id", record.ID)

		return
	}

	opts := &river.InsertOpts{
		Queue:       p.queueName,
		MaxAttempts: p.maxAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: uniqueByPeriodTranslation},
	}

	_, err = p.inserter.Insert(ctx, FeedbackTranslationArgs{
		FeedbackRecordID: record.ID,
		TargetLang:       targetLang,
		ValueTextHash:    translationValueTextHash(record.ValueText),
	}, opts)
	if err != nil {
		slog.Error("translation: enqueue failed",
			"event_id", event.ID, "feedback_record_id", record.ID, "error", err)

		return
	}

	slog.Info("translation: job enqueued",
		"event_id", event.ID, "feedback_record_id", record.ID, "target_lang", targetLang)
}

// translationValueTextHash hashes the trimmed, NFC-normalized value_text for dedupe;
// empty/nil returns "empty".
func translationValueTextHash(valueText *string) string {
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
