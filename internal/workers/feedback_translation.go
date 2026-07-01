package workers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/text/language"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// FeedbackTranslationWorker translates a feedback record's value_text into the tenant's target
// language and stores it — a configured EnrichmentWorker. It borrows the shared rate-limit snooze
// (it calls a rate-limited LLM provider) and uses the supersession skip: a stale-target write is a
// no-op once a newer-target job owns the row.
type FeedbackTranslationWorker = EnrichmentWorker[service.FeedbackTranslationArgs, string]

// translationWorkerService is the minimal interface the worker needs.
type translationWorkerService interface {
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	SetTranslation(ctx context.Context, feedbackRecordID uuid.UUID, translated *string, langKey string) error
}

const feedbackTranslationTimeout = 30 * time.Second

// NewFeedbackTranslationWorker creates a worker that fetches the record, translates its value_text
// into the target language (or copies it when the source already matches), and stores the result.
// metrics may be nil when metrics are disabled.
func NewFeedbackTranslationWorker(
	svc translationWorkerService, client service.TranslationClient, metrics observability.TranslationMetrics,
) *FeedbackTranslationWorker {
	return newEnrichmentWorker(enrichmentWorkerConfig[service.FeedbackTranslationArgs, string]{
		name:       "translation",
		timeout:    feedbackTranslationTimeout,
		recordID:   func(args service.FeedbackTranslationArgs) uuid.UUID { return args.FeedbackRecordID },
		getRecord:  svc.GetFeedbackRecord,
		eligible:   translationWorkerEligible,
		hasContent: translationWorkerHasContent,
		classify: func(ctx context.Context, record *models.FeedbackRecord, args service.FeedbackTranslationArgs) (string, error) {
			return translate(ctx, client, record, args.TargetLang)
		},
		persist: func(ctx context.Context, id uuid.UUID, args service.FeedbackTranslationArgs, translated *string) error {
			if translated == nil {
				return svc.SetTranslation(ctx, id, nil, "")
			}

			return svc.SetTranslation(ctx, id, translated, args.TargetLang)
		},
		// A stale-target write (the tenant's target changed, or this job came from a stale settings
		// cache) is a benign no-op — a newer-target job owns the row. Record it under a distinct
		// label so target churn / cache staleness stays observable.
		isSuperseded:     func(err error) bool { return errors.Is(err, huberrors.ErrTranslationSuperseded) },
		supersededReason: "superseded",
		rateLimited:      true,
		apiErrorReason:   "translation_api_failed",
		classifyErrVerb:  "translate",
		metrics:          translationWorkerMetrics(metrics),
	})
}

// translationWorkerEligible reports whether a record can be translated: only text fields carry open text.
func translationWorkerEligible(record *models.FeedbackRecord) bool {
	return record.FieldType == models.FieldTypeText
}

// translationWorkerHasContent reports whether a record has non-empty open text to translate.
func translationWorkerHasContent(record *models.FeedbackRecord) bool {
	return record.ValueText != nil && strings.TrimSpace(*record.ValueText) != ""
}

// translate returns the translated value_text, short-circuiting (copying the original) when the
// record's source language already matches the target language.
func translate(
	ctx context.Context, client service.TranslationClient, record *models.FeedbackRecord, targetLang string,
) (string, error) {
	sourceLang := ""
	if record.Language != nil {
		sourceLang = *record.Language
	}

	if sameLanguageAndScript(sourceLang, targetLang) {
		slog.Info("translation: source already in target language, copying value_text",
			"feedback_record_id", record.ID)

		return *record.ValueText, nil
	}

	translated, err := client.Translate(ctx, service.TranslateRequest{
		Text:       *record.ValueText,
		SourceLang: sourceLang,
		TargetLang: targetLang,
	})
	if err != nil {
		return "", fmt.Errorf("translation client: %w", err)
	}

	return translated, nil
}

// translationWorkerMetrics adapts TranslationMetrics to the worker's type-agnostic metric hooks,
// installing no-ops when metrics are disabled so the worker never nil-checks.
func translationWorkerMetrics(m observability.TranslationMetrics) enrichmentWorkerMetrics {
	if m == nil {
		return enrichmentWorkerMetrics{
			outcome:     func(context.Context, string) {},
			duration:    func(context.Context, time.Duration, string) {},
			workerError: func(context.Context, string) {},
		}
	}

	return enrichmentWorkerMetrics{
		outcome:     m.RecordTranslationOutcome,
		duration:    m.RecordTranslationDuration,
		workerError: m.RecordWorkerError,
	}
}

// sameLanguageAndScript reports whether two BCP-47 tags share both base language and script, so
// en-US and en-GB match (copying the source is safe) but zh-Hans and zh-Hant do not (mutually
// unintelligible scripts must be translated). An empty or unparseable tag is treated as not
// matching, so an unknown source always translates.
func sameLanguageAndScript(source, target string) bool {
	if strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" {
		return false
	}

	sourceTag, errSource := language.Parse(source)
	targetTag, errTarget := language.Parse(target)

	if errSource != nil || errTarget != nil {
		return false
	}

	// "und" (and similar) coerce to a guessed base via likely-subtags; never treat an undetermined
	// source or target as a match — translate instead of copying.
	if sourceTag == language.Und || targetTag == language.Und {
		return false
	}

	sourceBase, _ := sourceTag.Base()
	targetBase, _ := targetTag.Base()

	if sourceBase != targetBase {
		return false
	}

	sourceScript, _ := sourceTag.Script()
	targetScript, _ := targetTag.Script()

	return sourceScript == targetScript
}
