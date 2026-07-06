package workers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/text/language"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// FeedbackTranslationWorker translates a feedback record's value_text into the tenant's target
// language and stores it — a configured enrichmentWorker. It borrows the shared rate-limit snooze
// (it calls a rate-limited LLM provider) and uses the supersession skip: a stale-target OR
// stale-content write is a no-op once a newer job owns the row.
type FeedbackTranslationWorker = enrichmentWorker[service.FeedbackTranslationArgs, string]

// translationWorkerService is the minimal interface the worker needs.
type translationWorkerService interface {
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	SetTranslation(ctx context.Context, feedbackRecordID uuid.UUID, translated *string, langKey string,
		stillCurrent func(valueText *string) bool) error
}

// NewFeedbackTranslationWorker creates a worker that fetches the record, translates its value_text
// into the target language (or copies it when the source already matches), and stores the result.
// metrics may be nil when metrics are disabled.
func NewFeedbackTranslationWorker(
	svc translationWorkerService, client service.TranslationClient, metrics observability.TranslationMetrics,
) *FeedbackTranslationWorker {
	return newEnrichmentWorker(enrichmentWorkerConfig[service.FeedbackTranslationArgs, string]{
		name:       "translation",
		timeout:    enrichmentJobTimeout,
		recordID:   func(args service.FeedbackTranslationArgs) uuid.UUID { return args.FeedbackRecordID },
		getRecord:  svc.GetFeedbackRecord,
		eligible:   (*models.FeedbackRecord).IsTextField,
		hasContent: (*models.FeedbackRecord).HasOpenText,
		classify: func(ctx context.Context, record *models.FeedbackRecord, args service.FeedbackTranslationArgs) (string, error) {
			return translate(ctx, client, record, args.TargetLang)
		},
		persist: func(ctx context.Context, record *models.FeedbackRecord, args service.FeedbackTranslationArgs, translated *string) error {
			// Guard the write against content churn since the Work-time read: a stale job's
			// translation (or clear) must not land last over a newer job's write.
			stillCurrent := valueTextStillCurrent(record.ValueText)
			if translated == nil {
				return svc.SetTranslation(ctx, record.ID, nil, "", stillCurrent)
			}

			return svc.SetTranslation(ctx, record.ID, translated, args.TargetLang, stillCurrent)
		},
		// A stale write (the tenant's target changed, this job came from a stale settings cache,
		// or the record's content changed mid-job) is a benign no-op — a newer job owns the row.
		// Record it under a distinct label so target/content churn stays observable.
		isSuperseded:     func(err error) bool { return errors.Is(err, huberrors.ErrTranslationSuperseded) },
		supersededReason: "superseded",
		rateLimited:      true,
		apiErrorReason:   "translation_api_failed",
		classifyErrVerb:  "translate",
		metrics:          translationWorkerMetrics(metrics),
	})
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
		return noopEnrichmentWorkerMetrics
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

	// Never treat an undetermined language as a match — translate instead of copying. The bare
	// "und" tag is checked directly, but compound und tags (und-Latn, und-DE, ...) parse to a
	// GUESSED base via likely-subtags (und-Latn -> en with High confidence), so requiring Exact
	// base confidence on both sides is what actually keeps "unknown language, Latin script"
	// feedback from being copied through untranslated as the target language. Real tags — incl.
	// deprecated aliases like iw -> he — canonicalize with Exact confidence, so they still match.
	if sourceTag == language.Und || targetTag == language.Und {
		return false
	}

	sourceBase, sourceConf := sourceTag.Base()
	targetBase, targetConf := targetTag.Base()

	if sourceConf != language.Exact || targetConf != language.Exact {
		return false
	}

	if sourceBase != targetBase {
		return false
	}

	sourceScript, _ := sourceTag.Script()
	targetScript, _ := targetTag.Script()

	return sourceScript == targetScript
}
