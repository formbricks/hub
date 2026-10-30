package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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
	// first rotates which target is served first, so a lane that hovers one short of its target
	// depth cannot hand its single slot to the same target every time.
	mu    sync.Mutex
	first int
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
// with less backlog than its share leaves the rest to the targets after it. The first target
// rotates between sweeps. So a large taxonomy backlog cannot starve search-embedding repair, or
// the reverse.
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

	if len(s.targets) == 0 {
		return result, nil
	}

	retryBefore := time.Now().Add(-s.retryAfter)
	params := make([]river.InsertManyParams, 0, room)

	first := s.nextFirstTarget()

	for i := range s.targets {
		target := s.targets[(first+i)%len(s.targets)]

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

// nextFirstTarget returns the target this sweep serves first and advances the rotation.
func (s *EmbeddingReconcileService) nextFirstTarget() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	first := s.first
	s.first = (s.first + 1) % len(s.targets)

	return first
}

// repairJob is one record's repair on the low-priority repair lane.
func (s *EmbeddingReconcileService) repairJob(target EmbeddingReconcileTarget, id uuid.UUID) river.InsertManyParams {
	return repairEmbeddingJob(id, target, EmbeddingsReconcileQueueName, embeddingRepairPriority, s.maxAttempts)
}

// repairEmbeddingJob is the one shape of a repair job, for the sweep and the operator backfill
// alike. Both use ValueTextHashRepair and the in-flight-only unique options, so their jobs for the
// same record are one River unique key (the queue is not part of it) and neither can queue a repair
// the other already has in flight. priority 0 is River's default.
func repairEmbeddingJob(
	id uuid.UUID, target EmbeddingReconcileTarget, queue string, priority, maxAttempts int,
) river.InsertManyParams {
	return river.InsertManyParams{
		Args: FeedbackEmbeddingArgs{
			FeedbackRecordID: id,
			Model:            target.Model,
			InputKind:        models.NormalizeEmbeddingInputKind(target.InputKind),
			ValueTextHash:    ValueTextHashRepair,
		},
		InsertOpts: &river.InsertOpts{
			Queue:       queue,
			Priority:    priority,
			MaxAttempts: maxAttempts,
			UniqueOpts:  inFlightUniqueOpts(),
		},
	}
}

// inFlightUniqueOpts dedupes a repair or backfill job against the same job still in flight, and
// only that: a completed job must not swallow a later repair of the same record, which River's
// default unique states (they include completed) would do for as long as the row is retained.
func inFlightUniqueOpts() river.UniqueOpts {
	return river.UniqueOpts{
		ByArgs: true,
		ByState: []rivertype.JobState{
			rivertype.JobStateAvailable,
			rivertype.JobStatePending,
			rivertype.JobStateRetryable,
			rivertype.JobStateRunning,
			rivertype.JobStateScheduled,
		},
	}
}
