package workers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"golang.org/x/text/language"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// FeedbackTranslationWorker translates a feedback record's value_text into the tenant's
// target language and stores it, mirroring the embedding worker's error handling.
type FeedbackTranslationWorker struct {
	river.WorkerDefaults[service.FeedbackTranslationArgs]

	service translationWorkerService
	client  service.TranslationClient
	metrics observability.TranslationMetrics
}

// translationWorkerService is the minimal interface the worker needs.
type translationWorkerService interface {
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	SetTranslation(ctx context.Context, feedbackRecordID uuid.UUID, translated *string, langKey string) error
}

// NewFeedbackTranslationWorker creates a worker that fetches the record, translates its
// value_text, and stores the result. metrics may be nil when metrics are disabled.
func NewFeedbackTranslationWorker(
	svc translationWorkerService, client service.TranslationClient, metrics observability.TranslationMetrics,
) *FeedbackTranslationWorker {
	return &FeedbackTranslationWorker{service: svc, client: client, metrics: metrics}
}

const feedbackTranslationTimeout = 30 * time.Second

// Timeout limits how long a single translation job can run.
func (w *FeedbackTranslationWorker) Timeout(*river.Job[service.FeedbackTranslationArgs]) time.Duration {
	return feedbackTranslationTimeout
}

// Work loads the record, translates value_text into the target language (or copies it
// when the source already matches), and persists the result.
func (w *FeedbackTranslationWorker) Work(ctx context.Context, job *river.Job[service.FeedbackTranslationArgs]) error {
	args := job.Args
	start := time.Now()

	record, err := w.service.GetFeedbackRecord(ctx, args.FeedbackRecordID)
	if err != nil {
		// Not-found means the record was deleted or its tenant purged between enqueue and
		// now: a benign race, not a failure. Record it as skipped (consistent with the
		// not-found-on-write path) so it does not trip failure alerts.
		if errors.Is(err, huberrors.ErrNotFound) {
			if w.metrics != nil {
				w.metrics.RecordTranslationOutcome(ctx, "skipped")
				w.metrics.RecordTranslationDuration(ctx, time.Since(start), "skipped")
			}

			slog.Info("translation: record gone before translate, skipping",
				"feedback_record_id", args.FeedbackRecordID)

			return nil
		}

		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "get_record_failed")
			w.metrics.RecordTranslationOutcome(ctx, "failed_final")
			w.metrics.RecordTranslationDuration(ctx, time.Since(start), "failed_final")
		}

		slog.Error("translation: get record failed", "feedback_record_id", args.FeedbackRecordID, "error", err)

		return fmt.Errorf("get feedback record: %w", err)
	}

	if record.FieldType != models.FieldTypeText {
		if w.metrics != nil {
			w.metrics.RecordTranslationOutcome(ctx, "skipped")
			w.metrics.RecordTranslationDuration(ctx, time.Since(start), "skipped")
		}

		slog.Info("translation: skipped, not a text field", "feedback_record_id", args.FeedbackRecordID)

		return nil
	}

	// value_text became empty since enqueue (e.g. an edit cleared it): clear any stale
	// translation rather than translate empty text (mirrors the embedding worker).
	if record.ValueText == nil || strings.TrimSpace(*record.ValueText) == "" {
		if err := w.service.SetTranslation(ctx, args.FeedbackRecordID, nil, ""); err != nil {
			return w.handleSetTranslationError(ctx, err, args.FeedbackRecordID, start, job.Attempt >= job.MaxAttempts)
		}

		if w.metrics != nil {
			w.metrics.RecordTranslationOutcome(ctx, "success")
			w.metrics.RecordTranslationDuration(ctx, time.Since(start), "success")
		}

		slog.Info("translation: cleared (empty value_text)", "feedback_record_id", args.FeedbackRecordID)

		return nil
	}

	translated, err := w.translate(ctx, record, args.TargetLang)
	if err != nil {
		return w.handleTranslateError(ctx, err, args.FeedbackRecordID, start, job)
	}

	if err := w.service.SetTranslation(ctx, args.FeedbackRecordID, &translated, args.TargetLang); err != nil {
		return w.handleSetTranslationError(ctx, err, args.FeedbackRecordID, start, job.Attempt >= job.MaxAttempts)
	}

	if w.metrics != nil {
		w.metrics.RecordTranslationOutcome(ctx, "success")
		w.metrics.RecordTranslationDuration(ctx, time.Since(start), "success")
	}

	slog.Info("translation: stored",
		"feedback_record_id", args.FeedbackRecordID, "target_lang", args.TargetLang)

	return nil
}

// handleTranslateError maps a translation provider failure to the right worker outcome: a
// rate-limit 429 snoozes (re-queues without consuming an attempt, so a burst defers rather than
// drops work); any other error retries until the attempts are spent, then fails.
func (w *FeedbackTranslationWorker) handleTranslateError(
	ctx context.Context, err error, feedbackRecordID uuid.UUID, start time.Time,
	job *river.Job[service.FeedbackTranslationArgs],
) error {
	if delay, ok := rateLimitSnoozeDelay(err, job.CreatedAt); ok {
		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "rate_limited")
			w.metrics.RecordTranslationOutcome(ctx, "retry")
			w.metrics.RecordTranslationDuration(ctx, time.Since(start), "retry")
		}

		slog.Warn("translation: provider rate-limited, snoozing",
			"feedback_record_id", feedbackRecordID, "retry_after", delay.String())

		//nolint:wrapcheck // river sentinel: JobSnooze must be returned unwrapped for River to detect the snooze
		return river.JobSnooze(delay)
	}

	isLastAttempt := job.Attempt >= job.MaxAttempts
	outcome := "retry"

	if isLastAttempt {
		outcome = "failed_final"
	}

	if w.metrics != nil {
		w.metrics.RecordWorkerError(ctx, "translation_api_failed")
		w.metrics.RecordTranslationOutcome(ctx, outcome)
		w.metrics.RecordTranslationDuration(ctx, time.Since(start), outcome)
	}

	if isLastAttempt {
		slog.Error("translation: provider failed (final attempt)",
			"feedback_record_id", feedbackRecordID, "error", err)

		return fmt.Errorf("translate (final attempt): %w", err)
	}

	return fmt.Errorf("translate: %w", err)
}

// translate returns the translated value_text, short-circuiting (copying the original)
// when the record's source language already matches the target language.
func (w *FeedbackTranslationWorker) translate(
	ctx context.Context, record *models.FeedbackRecord, targetLang string,
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

	translated, err := w.client.Translate(ctx, service.TranslateRequest{
		Text:       *record.ValueText,
		SourceLang: sourceLang,
		TargetLang: targetLang,
	})
	if err != nil {
		return "", fmt.Errorf("translation client: %w", err)
	}

	return translated, nil
}

// handleSetTranslationError maps a translation write failure to a worker outcome,
// mirroring the embedding worker: a missing record completes the job (nothing to
// write), a tenant write conflict retries (the post-purge attempt finds the record
// gone), and anything else fails the job.
func (w *FeedbackTranslationWorker) handleSetTranslationError(
	ctx context.Context, err error, feedbackRecordID uuid.UUID, start time.Time, isLastAttempt bool,
) error {
	switch {
	case errors.Is(err, huberrors.ErrTranslationSuperseded):
		// The tenant's target language changed (or this job was enqueued from a stale
		// settings cache) before the write landed: a newer-target job owns the row, so this
		// stale-target write was a no-op. Benign — record as skipped, with a distinct worker
		// label so target churn / cache staleness stays observable; do not retry or fail.
		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "superseded")
			w.metrics.RecordTranslationOutcome(ctx, "skipped")
			w.metrics.RecordTranslationDuration(ctx, time.Since(start), "skipped")
		}

		slog.Info("translation: superseded by newer target, skipping write", "feedback_record_id", feedbackRecordID)

		return nil
	case errors.Is(err, huberrors.ErrNotFound):
		if w.metrics != nil {
			w.metrics.RecordTranslationOutcome(ctx, "skipped")
			w.metrics.RecordTranslationDuration(ctx, time.Since(start), "skipped")
		}

		slog.Info("translation: record gone before write, skipping", "feedback_record_id", feedbackRecordID)

		return nil
	case errors.Is(err, huberrors.ErrTenantWriteConflict):
		outcome := "retry"
		if isLastAttempt {
			outcome = "failed_final"
		}

		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "tenant_write_conflict")
			w.metrics.RecordTranslationOutcome(ctx, outcome)
			w.metrics.RecordTranslationDuration(ctx, time.Since(start), outcome)
		}

		slog.Warn("translation: tenant data purge in progress, deferring write",
			"feedback_record_id", feedbackRecordID, "final_attempt", isLastAttempt)

		return fmt.Errorf("set feedback record translation: %w", err)
	default:
		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "update_failed")
			w.metrics.RecordTranslationOutcome(ctx, "failed_final")
			w.metrics.RecordTranslationDuration(ctx, time.Since(start), "failed_final")
		}

		slog.Error("translation: set translation failed",
			"feedback_record_id", feedbackRecordID, "error", err)

		return fmt.Errorf("set feedback record translation: %w", err)
	}
}

// sameLanguageAndScript reports whether two BCP-47 tags share both base language and
// script, so en-US and en-GB match (copying the source is safe) but zh-Hans and
// zh-Hant do not (mutually unintelligible scripts must be translated). An empty or
// unparseable tag is treated as not matching, so an unknown source always translates.
func sameLanguageAndScript(source, target string) bool {
	if strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" {
		return false
	}

	sourceTag, errSource := language.Parse(source)
	targetTag, errTarget := language.Parse(target)

	if errSource != nil || errTarget != nil {
		return false
	}

	// "und" (and similar) coerce to a guessed base via likely-subtags; never treat an
	// undetermined source or target as a match — translate instead of copying.
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
