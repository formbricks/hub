package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/formbricks/hub/internal/models"
)

// ErrEmbeddingReconcileInserterUnset reports a sweep invoked before the River client was attached.
var ErrEmbeddingReconcileInserterUnset = errors.New("embedding reconcile: inserter not set")

const embeddingRepairPriority = 4

// EmbeddingReconcileRepository is the data boundary needed by the taxonomy embedding sweep.
type EmbeddingReconcileRepository interface {
	ListPendingTaxonomyEmbeddingIDs(
		ctx context.Context, model string, retryBefore time.Time, limit int,
	) ([]uuid.UUID, error)
	CountRunnableEmbeddingJobs(ctx context.Context, queue string) (int, error)
}

// EmbeddingReconcileService keeps the low-priority repair lane topped up without materializing the
// complete deployment backlog or crowding live embedding work.
type EmbeddingReconcileService struct {
	repo        EmbeddingReconcileRepository
	inserter    RiverBatchInserter
	model       string
	targetDepth int
	maxAttempts int
	retryAfter  time.Duration
}

// NewEmbeddingReconcileService creates a level-triggered taxonomy embedding reconciler.
func NewEmbeddingReconcileService(
	repo EmbeddingReconcileRepository,
	model string,
	targetDepth int,
	maxAttempts int,
	retryAfter time.Duration,
) *EmbeddingReconcileService {
	return &EmbeddingReconcileService{
		repo:        repo,
		model:       model,
		targetDepth: targetDepth,
		maxAttempts: maxAttempts,
		retryAfter:  retryAfter,
	}
}

// SetInserter attaches River after its worker registry has been built.
func (s *EmbeddingReconcileService) SetInserter(inserter RiverBatchInserter) {
	s.inserter = inserter
}

// EmbeddingReconcileResult reports one sweep's bounded queue action.
type EmbeddingReconcileResult struct {
	Found    int
	Enqueued int
	Depth    int
	AtTarget bool
}

// Sweep tops the repair queue up to targetDepth with records that are still missing their current
// taxonomy embedding. The query excludes active jobs across all queues and terminal failures,
// and cools transient failures before making them eligible again.
func (s *EmbeddingReconcileService) Sweep(ctx context.Context) (EmbeddingReconcileResult, error) {
	result := EmbeddingReconcileResult{}
	if s.inserter == nil {
		return result, ErrEmbeddingReconcileInserterUnset
	}

	depth, err := s.repo.CountRunnableEmbeddingJobs(ctx, EmbeddingsReconcileQueueName)
	if err != nil {
		return result, fmt.Errorf("read repair queue depth: %w", err)
	}

	result.Depth = depth

	room := s.targetDepth - depth
	if room <= 0 {
		result.AtTarget = true

		return result, nil
	}

	ids, err := s.repo.ListPendingTaxonomyEmbeddingIDs(ctx, s.model, time.Now().Add(-s.retryAfter), room)
	if err != nil {
		return result, fmt.Errorf("list pending taxonomy embeddings: %w", err)
	}

	result.Found = len(ids)

	if len(ids) == 0 {
		return result, nil
	}

	params := make([]river.InsertManyParams, 0, len(ids))
	for _, id := range ids {
		params = append(params, river.InsertManyParams{
			Args: FeedbackEmbeddingArgs{
				FeedbackRecordID: id,
				Model:            s.model,
				InputKind:        models.EmbeddingInputKindTaxonomyTranslated,
				ValueTextHash:    "reconcile",
			},
			InsertOpts: &river.InsertOpts{
				Queue:       EmbeddingsReconcileQueueName,
				Priority:    embeddingRepairPriority,
				MaxAttempts: s.maxAttempts,
				UniqueOpts: river.UniqueOpts{
					ByArgs: true,
					ByState: []rivertype.JobState{
						rivertype.JobStateAvailable,
						rivertype.JobStatePending,
						rivertype.JobStateRetryable,
						rivertype.JobStateRunning,
						rivertype.JobStateScheduled,
					},
				},
			},
		})
	}

	results, err := s.inserter.InsertMany(ctx, params)
	if err != nil {
		return result, fmt.Errorf("enqueue taxonomy embedding repairs: %w", err)
	}

	for _, inserted := range results {
		if inserted != nil && !inserted.UniqueSkippedAsDuplicate {
			result.Enqueued++
		}
	}

	slog.InfoContext(ctx, "embedding reconcile: repair jobs enqueued",
		"found", result.Found,
		"enqueued", result.Enqueued,
		"queue_depth_before", result.Depth,
	)

	return result, nil
}
