package jobs

import (
	"context"
	"errors"
	"log/slog"

	"github.com/formbricks/hub/internal/embeddings"
	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"golang.org/x/time/rate"
)

// EmbeddingUpdater is an interface for updating embeddings on records.
// This allows the worker to update any record type without knowing the concrete implementation.
type EmbeddingUpdater interface {
	UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error
}

// EmbeddingWorkerDeps holds the dependencies for the embedding worker.
type EmbeddingWorkerDeps struct {
	EmbeddingClient  embeddings.Client
	FeedbackUpdater  EmbeddingUpdater
	TopicUpdater     EmbeddingUpdater
	KnowledgeUpdater EmbeddingUpdater
	RateLimiter      *rate.Limiter
}

// EmbeddingWorker processes embedding generation jobs.
type EmbeddingWorker struct {
	river.WorkerDefaults[EmbeddingJobArgs]
	deps EmbeddingWorkerDeps
}

// NewEmbeddingWorker creates a new embedding worker with the given dependencies.
func NewEmbeddingWorker(deps EmbeddingWorkerDeps) *EmbeddingWorker {
	return &EmbeddingWorker{deps: deps}
}

// Work processes an embedding job.
func (w *EmbeddingWorker) Work(ctx context.Context, job *river.Job[EmbeddingJobArgs]) error {
	args := job.Args

	slog.Debug("processing embedding job",
		"job_id", job.ID,
		"record_type", args.RecordType,
		"record_id", args.RecordID,
		"text_length", len(args.Text),
	)

	// Wait for rate limit token if configured
	if w.deps.RateLimiter != nil {
		if err := w.deps.RateLimiter.Wait(ctx); err != nil {
			return err
		}
	}

	// Generate embedding
	embedding, err := w.deps.EmbeddingClient.GetEmbedding(ctx, args.Text)
	if err != nil {
		slog.Error("failed to generate embedding",
			"job_id", job.ID,
			"record_type", args.RecordType,
			"record_id", args.RecordID,
			"error", err,
		)
		return err // River will retry based on configuration
	}

	// Get the appropriate updater based on record type
	updater := w.getUpdater(args.RecordType)
	if updater == nil {
		slog.Error("unknown record type",
			"job_id", job.ID,
			"record_type", args.RecordType,
		)
		// Return nil to mark job as complete - unknown type won't be fixed by retry
		return nil
	}

	// Update the record with the embedding
	err = updater.UpdateEmbedding(ctx, args.RecordID, embedding)
	if err != nil {
		// Check if record was deleted
		var notFoundErr *apperrors.NotFoundError
		if errors.As(err, &notFoundErr) {
			slog.Info("record deleted before embedding job completed",
				"job_id", job.ID,
				"record_type", args.RecordType,
				"record_id", args.RecordID,
			)
			// Return nil to mark job as complete - record no longer exists
			return nil
		}

		slog.Error("failed to update embedding",
			"job_id", job.ID,
			"record_type", args.RecordType,
			"record_id", args.RecordID,
			"error", err,
		)
		return err // Retry on other errors
	}

	slog.Info("embedding generated successfully",
		"job_id", job.ID,
		"record_type", args.RecordType,
		"record_id", args.RecordID,
	)

	return nil
}

// getUpdater returns the appropriate updater for the given record type.
func (w *EmbeddingWorker) getUpdater(recordType string) EmbeddingUpdater {
	switch recordType {
	case RecordTypeFeedback:
		return w.deps.FeedbackUpdater
	case RecordTypeTopic:
		return w.deps.TopicUpdater
	case RecordTypeKnowledge:
		return w.deps.KnowledgeUpdater
	default:
		return nil
	}
}
