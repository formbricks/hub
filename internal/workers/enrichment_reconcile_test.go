package workers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
)

// stubSweeper records the deadline it was handed and returns a canned outcome.
type stubSweeper struct {
	result   service.ReconcileResult
	err      error
	deadline time.Time
	hadDDL   bool
}

func (s *stubSweeper) Sweep(ctx context.Context) (service.ReconcileResult, error) {
	s.deadline, s.hadDDL = ctx.Deadline()

	return s.result, s.err
}

// TestEnrichmentReconcileWorkerTimeout binds the sweep's own deadline to the worker.
//
// River only honours enrichmentReconcileTimeout because this method exists: WorkerDefaults.Timeout
// returns zero, River reads zero as "use Config.JobTimeout", RIVER_JOB_TIMEOUT_SECONDS defaults to
// 0, and River in turn reads THAT as its own one-minute default. Delete this method and the sweep
// is cancelled at a minute -- the context.WithTimeout inside Work cannot rescue it, a child
// context being unable to outlive its parent -- and MaxAttempts 1 turns the cancellation into a
// discarded job. Nothing else in the suite would notice.
func TestEnrichmentReconcileWorkerTimeout(t *testing.T) {
	worker := workers.NewEnrichmentReconcileWorker(&stubSweeper{})

	assert.Equal(t, 5*time.Minute, worker.Timeout(nil),
		"the sweep's declared bound must be the one River applies, not River's one-minute default")
}

// TestEnrichmentReconcileWorkerBoundsTheSweep pins the second half of that guarantee: Work hands
// the sweeper a context that already carries the deadline, so a sweeper called directly (as tests
// and any future non-River caller do) is bounded too.
func TestEnrichmentReconcileWorkerBoundsTheSweep(t *testing.T) {
	sweeper := &stubSweeper{result: service.ReconcileResult{Enqueued: map[string]int{"sentiment": 3}}}
	worker := workers.NewEnrichmentReconcileWorker(sweeper)

	require.NoError(t, worker.Work(context.Background(), &river.Job[service.EnrichmentReconcileArgs]{}))

	require.True(t, sweeper.hadDDL, "the sweep runs under a deadline, never unbounded")
	assert.WithinDuration(t, time.Now().Add(5*time.Minute), sweeper.deadline, 30*time.Second)
}

// TestEnrichmentReconcileWorkerReturnsSweepFailures keeps the failure visible to River. The next
// tick is what makes the system converge, not this retry -- but a sweep that swallowed its error
// would report success while coverage silently stopped.
func TestEnrichmentReconcileWorkerReturnsSweepFailures(t *testing.T) {
	sentinel := errors.New("pending query failed")
	worker := workers.NewEnrichmentReconcileWorker(&stubSweeper{err: sentinel})

	err := worker.Work(context.Background(), &river.Job[service.EnrichmentReconcileArgs]{})

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel, "the cause survives the wrap so River and the logs agree on why")
}
