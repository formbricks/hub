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

// EmbeddingReconcileRepository is the data boundary needed by the embedding repair sweep.
type EmbeddingReconcileRepository interface {
	ListPendingEmbeddingIDs(
		ctx context.Context, model string, inputKind models.EmbeddingInputKind, retryBefore time.Time, limit int,
	) ([]uuid.UUID, error)
	CountRunnableEmbeddingJobs(ctx context.Context, queue string) (int, error)
}

// EmbeddingReconcileTarget is one embedding the sweep keeps complete: a model and the input kind
// its vectors are built from.
type EmbeddingReconcileTarget struct {
	Model     string
	InputKind models.EmbeddingInputKind
}

// EmbeddingReconcileService keeps the low-priority repair lane topped up without materializing the
// complete deployment backlog or crowding live embedding work. It repairs missing, stale and
// orphaned vectors for every target (see ListPendingEmbeddingIDs).
type EmbeddingReconcileService struct {
	repo        EmbeddingReconcileRepository
	inserter    RiverBatchInserter
	targets     []EmbeddingReconcileTarget
	targetDepth int
	maxAttempts int
	retryAfter  time.Duration
}

// NewEmbeddingReconcileService creates a level-triggered embedding reconciler for targets, in
// sweep order.
func NewEmbeddingReconcileService(
	repo EmbeddingReconcileRepository,
	targets []EmbeddingReconcileTarget,
	targetDepth int,
	maxAttempts int,
	retryAfter time.Duration,
) *EmbeddingReconcileService {
	return &EmbeddingReconcileService{
		repo:        repo,
		targets:     targets,
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

// Sweep tops the repair queue up to targetDepth with records whose vectors are missing, stale or
// orphaned. The query excludes active jobs across all queues and terminal failures, and cools
// transient failures before making them eligible again.
//
// The room is shared fairly: each target may claim an equal share of what is left, and a target
// with less backlog than its share leaves the rest to the targets after it. So a large taxonomy
// backlog cannot starve search-embedding repair, or the reverse.
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

	retryBefore := time.Now().Add(-s.retryAfter)
	params := make([]river.InsertManyParams, 0, room)

	for i, target := range s.targets {
		share := room / (len(s.targets) - i)
		if share <= 0 {
			continue
		}

		ids, err := s.repo.ListPendingEmbeddingIDs(ctx, target.Model, target.InputKind, retryBefore, share)
		if err != nil {
			return result, fmt.Errorf("list pending embeddings for %s: %w", target.InputKind, err)
		}

		room -= len(ids)

		for _, id := range ids {
			params = append(params, s.repairJob(target, id))
		}
	}

	result.Found = len(params)

	if len(params) == 0 {
		return result, nil
	}

	results, err := s.inserter.InsertMany(ctx, params)
	if err != nil {
		return result, fmt.Errorf("enqueue embedding repairs: %w", err)
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

// repairJob is one record's repair. Unique across the in-flight states so a sweep racing another
// sweep or a backfill cannot queue the same repair twice.
func (s *EmbeddingReconcileService) repairJob(target EmbeddingReconcileTarget, id uuid.UUID) river.InsertManyParams {
	return river.InsertManyParams{
		Args: FeedbackEmbeddingArgs{
			FeedbackRecordID: id,
			Model:            target.Model,
			InputKind:        models.NormalizeEmbeddingInputKind(target.InputKind),
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
	}
}
