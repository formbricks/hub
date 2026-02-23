// Package workers provides River job workers (e.g. webhook delivery, feedback embedding).
package workers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/openai"
	"github.com/formbricks/hub/internal/service"
)

// FeedbackEmbeddingWorker generates and stores embeddings for feedback records.
type FeedbackEmbeddingWorker struct {
	river.WorkerDefaults[service.FeedbackEmbeddingArgs]

	embeddingService feedbackEmbeddingService
	openaiClient     *openai.Client
	metrics          observability.EmbeddingMetrics
}

// feedbackEmbeddingService is the minimal interface needed by the worker.
type feedbackEmbeddingService interface {
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	SetFeedbackRecordEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error
}

// NewFeedbackEmbeddingWorker creates a worker that fetches the record, calls OpenAI, and updates the embedding.
// metrics may be nil when metrics are disabled.
func NewFeedbackEmbeddingWorker(
	embeddingService feedbackEmbeddingService,
	openaiClient *openai.Client,
	metrics observability.EmbeddingMetrics,
) *FeedbackEmbeddingWorker {
	return &FeedbackEmbeddingWorker{
		embeddingService: embeddingService,
		openaiClient:     openaiClient,
		metrics:          metrics,
	}
}

const feedbackEmbeddingTimeout = 30 * time.Second

// Timeout limits how long a single embedding job can run.
func (w *FeedbackEmbeddingWorker) Timeout(*river.Job[service.FeedbackEmbeddingArgs]) time.Duration {
	return feedbackEmbeddingTimeout
}

// Work loads the record, generates or clears the embedding, and persists it.
func (w *FeedbackEmbeddingWorker) Work(ctx context.Context, job *river.Job[service.FeedbackEmbeddingArgs]) error {
	args := job.Args
	start := time.Now()

	record, err := w.embeddingService.GetFeedbackRecord(ctx, args.FeedbackRecordID)
	if err != nil {
		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "get_record_failed")
			w.metrics.RecordEmbeddingOutcome(ctx, "failed_final")
			w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "failed_final")
		}

		slog.Error("embedding: get record failed",
			"feedback_record_id", args.FeedbackRecordID,
			"error", err,
		)

		return nil // no retry when record not found
	}

	text := ""
	if record.ValueText != nil {
		text = strings.TrimSpace(*record.ValueText)
	}

	if text == "" {
		return w.handleEmptyText(ctx, args.FeedbackRecordID, record, start)
	}

	embedding, err := w.openaiClient.CreateEmbedding(ctx, text)
	if err != nil {
		isLastAttempt := job.Attempt >= job.MaxAttempts

		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "openai_failed")

			if isLastAttempt {
				w.metrics.RecordEmbeddingOutcome(ctx, "failed_final")
				w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "failed_final")
			} else {
				w.metrics.RecordEmbeddingOutcome(ctx, "retry")
				w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "retry")
			}
		}

		if isLastAttempt {
			slog.Error("embedding: openai failed (final attempt)",
				"feedback_record_id", args.FeedbackRecordID,
				"error", err,
			)

			return nil
		}

		return fmt.Errorf("openai embedding: %w", err)
	}

	err = w.embeddingService.SetFeedbackRecordEmbedding(ctx, args.FeedbackRecordID, embedding)
	if err != nil {
		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "update_failed")
			w.metrics.RecordEmbeddingOutcome(ctx, "failed_final")
			w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "failed_final")
		}

		slog.Error("embedding: set embedding failed",
			"feedback_record_id", args.FeedbackRecordID,
			"error", err,
		)

		return fmt.Errorf("set feedback record embedding: %w", err)
	}

	slog.Info("embedding: stored",
		"feedback_record_id", args.FeedbackRecordID,
	)

	if w.metrics != nil {
		w.metrics.RecordEmbeddingOutcome(ctx, "success")
		w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "success")
	}

	return nil
}

// handleEmptyText clears the embedding for text fields when value_text is empty, or records skip for non-text.
func (w *FeedbackEmbeddingWorker) handleEmptyText(
	ctx context.Context,
	feedbackRecordID uuid.UUID,
	record *models.FeedbackRecord,
	start time.Time,
) error {
	if record.FieldType == models.FieldTypeText {
		err := w.embeddingService.SetFeedbackRecordEmbedding(ctx, feedbackRecordID, nil)
		if err != nil {
			if w.metrics != nil {
				w.metrics.RecordWorkerError(ctx, "update_failed")
				w.metrics.RecordEmbeddingOutcome(ctx, "failed_final")
				w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "failed_final")
			}

			slog.Error("embedding: clear failed",
				"feedback_record_id", feedbackRecordID,
				"error", err,
			)

			return fmt.Errorf("clear feedback record embedding: %w", err)
		}

		if w.metrics != nil {
			w.metrics.RecordEmbeddingOutcome(ctx, "success")
			w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "success")
		}

		slog.Info("embedding: cleared (empty value_text)",
			"feedback_record_id", feedbackRecordID,
		)

		return nil
	}

	if w.metrics != nil {
		w.metrics.RecordEmbeddingOutcome(ctx, "skipped")
		w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "skipped")
	}

	slog.Info("embedding: skipped (no value_text)",
		"feedback_record_id", feedbackRecordID,
	)

	return nil
}
