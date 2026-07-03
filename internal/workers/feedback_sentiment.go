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

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// FeedbackSentimentWorker classifies a feedback record's value_text into a sentiment label and
// score and stores it. It is the embedding-shaped sibling of the translation worker (no target
// language, no supersession), but borrows the translation worker's rate-limit snooze since it,
// too, calls a rate-limited LLM provider.
type FeedbackSentimentWorker struct {
	river.WorkerDefaults[service.FeedbackSentimentArgs]

	service  sentimentWorkerService
	resolver sentimentSettingsReader
	client   service.SentimentClient
	metrics  observability.SentimentMetrics
}

// sentimentWorkerService is the minimal interface the worker needs.
type sentimentWorkerService interface {
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	SetSentiment(ctx context.Context, feedbackRecordID uuid.UUID, sentiment *models.SentimentValue, score *float64) error
}

// sentimentSettingsReader resolves a tenant's enrichment settings. The worker re-checks the
// per-directory sentiment switch through it because the enqueue provider fails open on a
// settings-read error (it enqueues rather than dropping the event), which makes the worker the
// authoritative gate before the LLM call: a transient tenant_settings/cache outage can defer
// work, but can never enrich a tenant that turned sentiment off, nor permanently drop it.
type sentimentSettingsReader interface {
	GetSettings(ctx context.Context, tenantID string) (*models.TenantSettings, error)
}

// NewFeedbackSentimentWorker creates a worker that fetches the record, re-checks the per-directory
// sentiment gate, classifies its value_text, and stores the result. metrics may be nil when
// metrics are disabled.
func NewFeedbackSentimentWorker(
	svc sentimentWorkerService, resolver sentimentSettingsReader,
	client service.SentimentClient, metrics observability.SentimentMetrics,
) *FeedbackSentimentWorker {
	return &FeedbackSentimentWorker{service: svc, resolver: resolver, client: client, metrics: metrics}
}

const feedbackSentimentTimeout = 30 * time.Second

// Timeout limits how long a single sentiment job can run.
func (w *FeedbackSentimentWorker) Timeout(*river.Job[service.FeedbackSentimentArgs]) time.Duration {
	return feedbackSentimentTimeout
}

// Work loads the record, classifies value_text (or clears a stale sentiment when it is empty),
// and persists the result.
func (w *FeedbackSentimentWorker) Work(ctx context.Context, job *river.Job[service.FeedbackSentimentArgs]) error {
	args := job.Args
	start := time.Now()

	record, err := w.service.GetFeedbackRecord(ctx, args.FeedbackRecordID)
	if err != nil {
		// Not-found means the record was deleted or its tenant purged between enqueue and now:
		// a benign race, not a failure. Record it as skipped (consistent with the not-found-on-
		// write path) so it does not trip failure alerts.
		if errors.Is(err, huberrors.ErrNotFound) {
			w.recordOutcome(ctx, "skipped", start)
			slog.Info("sentiment: record gone before classify, skipping", "feedback_record_id", args.FeedbackRecordID)

			return nil
		}

		// A non-not-found read error is transient (e.g. a DB blip): River retries while attempts
		// remain, so only the last attempt is a final failure. Recording failed_final on every
		// attempt would overcount it.
		isLastAttempt := job.Attempt >= job.MaxAttempts

		outcome := "retry"
		if isLastAttempt {
			outcome = "failed_final"
		}

		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "get_record_failed")
		}

		w.recordOutcome(ctx, outcome, start)
		slog.Error("sentiment: get record failed",
			"feedback_record_id", args.FeedbackRecordID, "final_attempt", isLastAttempt, "error", err)

		return fmt.Errorf("get feedback record: %w", err)
	}

	if record.FieldType != models.FieldTypeText {
		w.recordOutcome(ctx, "skipped", start)
		slog.Info("sentiment: skipped, not a text field", "feedback_record_id", args.FeedbackRecordID)

		return nil
	}

	// Authoritative per-directory gate. The enqueue provider fails open on a settings-read error
	// (it enqueues rather than dropping), so re-check here before doing any work: a read error is
	// transient and retries; a tenant that turned sentiment off is skipped without classifying or
	// clearing (matching what the provider gate does when settings are readable).
	settings, err := w.resolver.GetSettings(ctx, record.TenantID)
	if err != nil {
		isLastAttempt := job.Attempt >= job.MaxAttempts

		outcome := "retry"
		if isLastAttempt {
			outcome = "failed_final"
		}

		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "settings_read_failed")
		}

		w.recordOutcome(ctx, outcome, start)
		slog.Error("sentiment: resolve tenant settings failed",
			"feedback_record_id", args.FeedbackRecordID, "final_attempt", isLastAttempt, "error", err)

		return fmt.Errorf("resolve tenant settings: %w", err)
	}

	if !settings.Settings.SentimentEnrichmentEnabled() {
		w.recordOutcome(ctx, "skipped", start)
		slog.Info("sentiment: skipped, disabled for tenant", "feedback_record_id", args.FeedbackRecordID)

		return nil
	}

	// value_text became empty since enqueue (e.g. an edit cleared it): clear any stale sentiment
	// rather than classify empty text (mirrors the embedding/translation workers).
	if record.ValueText == nil || strings.TrimSpace(*record.ValueText) == "" {
		if err := w.service.SetSentiment(ctx, args.FeedbackRecordID, nil, nil); err != nil {
			return w.handleSetSentimentError(ctx, err, args.FeedbackRecordID, start, job.Attempt >= job.MaxAttempts)
		}

		w.recordOutcome(ctx, "success", start)
		slog.Info("sentiment: cleared (empty value_text)", "feedback_record_id", args.FeedbackRecordID)

		return nil
	}

	sourceLang := ""
	if record.Language != nil {
		sourceLang = *record.Language
	}

	result, err := w.client.Classify(ctx, *record.ValueText, sourceLang)
	if err != nil {
		return w.handleClassifyError(ctx, err, args.FeedbackRecordID, start, job)
	}

	if err := w.service.SetSentiment(ctx, args.FeedbackRecordID, &result.Label, &result.Score); err != nil {
		return w.handleSetSentimentError(ctx, err, args.FeedbackRecordID, start, job.Attempt >= job.MaxAttempts)
	}

	w.recordOutcome(ctx, "success", start)
	slog.Info("sentiment: stored",
		"feedback_record_id", args.FeedbackRecordID, "sentiment", result.Label, "score", result.Score)

	return nil
}

// handleClassifyError maps a provider failure to the right worker outcome: a rate-limit 429
// snoozes (re-queues without consuming an attempt, so a burst defers rather than drops work);
// any other error retries until the attempts are spent, then fails.
func (w *FeedbackSentimentWorker) handleClassifyError(
	ctx context.Context, err error, feedbackRecordID uuid.UUID, start time.Time,
	job *river.Job[service.FeedbackSentimentArgs],
) error {
	if delay, ok := rateLimitSnoozeDelay(err, job.CreatedAt); ok {
		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "rate_limited")
		}

		w.recordOutcome(ctx, "retry", start)
		slog.Warn("sentiment: provider rate-limited, snoozing",
			"feedback_record_id", feedbackRecordID, "retry_after", delay.String())

		//nolint:wrapcheck // river sentinel: JobSnooze must be returned unwrapped for River to detect the snooze
		return river.JobSnooze(delay)
	}

	isLastAttempt := job.Attempt >= job.MaxAttempts

	if w.metrics != nil {
		w.metrics.RecordWorkerError(ctx, "sentiment_api_failed")
	}

	if isLastAttempt {
		w.recordOutcome(ctx, "failed_final", start)
		slog.Error("sentiment: provider failed (final attempt)", "feedback_record_id", feedbackRecordID, "error", err)

		return fmt.Errorf("classify (final attempt): %w", err)
	}

	w.recordOutcome(ctx, "retry", start)

	return fmt.Errorf("classify: %w", err)
}

// handleSetSentimentError maps a sentiment write failure to a worker outcome, mirroring the
// embedding worker: a missing record completes the job (nothing to write), a tenant write
// conflict retries (the post-purge attempt finds the record gone), and anything else retries
// until the attempts are spent, then fails. Sentiment has no supersession case (no per-tenant
// target to be invalidated).
func (w *FeedbackSentimentWorker) handleSetSentimentError(
	ctx context.Context, err error, feedbackRecordID uuid.UUID, start time.Time, isLastAttempt bool,
) error {
	switch {
	case errors.Is(err, huberrors.ErrNotFound):
		w.recordOutcome(ctx, "skipped", start)
		slog.Info("sentiment: record gone before write, skipping", "feedback_record_id", feedbackRecordID)

		return nil
	case errors.Is(err, huberrors.ErrTenantWriteConflict):
		outcome := "retry"
		if isLastAttempt {
			outcome = "failed_final"
		}

		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "tenant_write_conflict")
		}

		w.recordOutcome(ctx, outcome, start)
		slog.Warn("sentiment: tenant data purge in progress, deferring write",
			"feedback_record_id", feedbackRecordID, "final_attempt", isLastAttempt)

		return fmt.Errorf("set feedback record sentiment: %w", err)
	default:
		// A generic write failure is transient (e.g. a DB blip): River retries while attempts
		// remain, so only the last attempt is a final failure — recording failed_final on every
		// attempt would overcount it (matches the classify and tenant-write-conflict branches).
		outcome := "retry"
		if isLastAttempt {
			outcome = "failed_final"
		}

		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "update_failed")
		}

		w.recordOutcome(ctx, outcome, start)
		slog.Error("sentiment: set sentiment failed",
			"feedback_record_id", feedbackRecordID, "final_attempt", isLastAttempt, "error", err)

		return fmt.Errorf("set feedback record sentiment: %w", err)
	}
}

// recordOutcome records the job outcome and duration under the same status label (no-op when
// metrics are disabled).
func (w *FeedbackSentimentWorker) recordOutcome(ctx context.Context, status string, start time.Time) {
	if w.metrics == nil {
		return
	}

	w.metrics.RecordSentimentOutcome(ctx, status)
	w.metrics.RecordSentimentDuration(ctx, time.Since(start), status)
}
