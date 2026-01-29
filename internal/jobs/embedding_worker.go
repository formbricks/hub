package jobs

import (
	"context"
	"errors"
	"log/slog"

	"github.com/formbricks/hub/internal/embeddings"
	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"golang.org/x/time/rate"
)

// EmbeddingUpdater is an interface for updating embeddings on records.
// This allows the worker to update any record type without knowing the concrete implementation.
type EmbeddingUpdater interface {
	UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error
}

// TopicMatcher is an interface for finding similar topics for embedding-based assignment.
type TopicMatcher interface {
	FindMostSpecificTopic(ctx context.Context, embedding []float32, tenantID *string, minSimilarity float64) (*models.TopicMatch, error)
}

// FeedbackAssigner is an interface for assigning topics to feedback records.
type FeedbackAssigner interface {
	AssignTopic(ctx context.Context, id uuid.UUID, topicID uuid.UUID, confidence float64) error
}

// DefaultMinSimilarity is the default threshold for topic assignment.
// Feedback must be at least this similar to a topic centroid to be assigned.
const DefaultMinSimilarity = 0.35

// EmbeddingWorkerDeps holds the dependencies for the embedding worker.
type EmbeddingWorkerDeps struct {
	EmbeddingClient  embeddings.Client
	FeedbackUpdater  EmbeddingUpdater
	TopicUpdater     EmbeddingUpdater
	KnowledgeUpdater EmbeddingUpdater
	RateLimiter      *rate.Limiter
	// Optional: for real-time topic assignment after embedding generation
	TopicMatcher     TopicMatcher
	FeedbackAssigner FeedbackAssigner
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

	// For feedback records, attempt real-time topic assignment
	if args.RecordType == RecordTypeFeedback && w.deps.TopicMatcher != nil && w.deps.FeedbackAssigner != nil {
		w.assignTopicToFeedback(ctx, args.RecordID, embedding, args.TenantID)
	}

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

// assignTopicToFeedback attempts to assign a topic to a feedback record based on embedding similarity.
// This is called after embedding generation succeeds for feedback records.
// Failures are logged but don't fail the job - embedding was already saved successfully.
func (w *EmbeddingWorker) assignTopicToFeedback(ctx context.Context, recordID uuid.UUID, embedding []float32, tenantID *string) {
	match, err := w.deps.TopicMatcher.FindMostSpecificTopic(ctx, embedding, tenantID, DefaultMinSimilarity)
	if err != nil {
		slog.Warn("topic matching failed",
			"record_id", recordID,
			"error", err,
		)
		return // Don't fail the job - embedding was successful
	}

	if match == nil {
		slog.Debug("no matching topic found",
			"record_id", recordID,
			"min_similarity", DefaultMinSimilarity,
		)
		return // No topics exist yet or none above threshold
	}

	if err := w.deps.FeedbackAssigner.AssignTopic(ctx, recordID, match.TopicID, match.Similarity); err != nil {
		slog.Warn("topic assignment failed",
			"record_id", recordID,
			"topic_id", match.TopicID,
			"error", err,
		)
		return // Don't fail - embedding was successful, assignment can be retried
	}

	slog.Info("topic assigned to feedback",
		"record_id", recordID,
		"topic_id", match.TopicID,
		"topic_title", match.Title,
		"confidence", match.Similarity,
	)
}
