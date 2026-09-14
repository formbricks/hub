package workers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
)

// stubSweeper records the deadline it was handed and returns a canned outcome.
type stubSweeper struct {
	result   service.ReconcileResult
	err      error
	deadline time.Time
	hadDDL   bool
	// takes makes the sweep occupy measurable time, so a duration assertion binds "this measures
	// the sweep" rather than "the clock happened to tick" -- an instant stub can legitimately
	// record 0s on a coarse clock, which is a flaky test, not a real signal.
	takes time.Duration
}

func (s *stubSweeper) Sweep(ctx context.Context) (service.ReconcileResult, error) {
	s.deadline, s.hadDDL = ctx.Deadline()

	if s.takes > 0 {
		time.Sleep(s.takes)
	}

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
	worker := workers.NewEnrichmentReconcileWorker(&stubSweeper{}, nil)

	assert.Equal(t, 5*time.Minute, worker.Timeout(nil),
		"the sweep's declared bound must be the one River applies, not River's one-minute default")
}

// TestEnrichmentReconcileWorkerBoundsTheSweep pins the second half of that guarantee: Work hands
// the sweeper a context that already carries the deadline, so a sweeper called directly (as tests
// and any future non-River caller do) is bounded too.
func TestEnrichmentReconcileWorkerBoundsTheSweep(t *testing.T) {
	sweeper := &stubSweeper{result: service.ReconcileResult{Enqueued: map[string]int{"sentiment": 3}}}
	worker := workers.NewEnrichmentReconcileWorker(sweeper, nil)

	require.NoError(t, worker.Work(context.Background(), &river.Job[service.EnrichmentReconcileArgs]{}))

	require.True(t, sweeper.hadDDL, "the sweep runs under a deadline, never unbounded")
	assert.WithinDuration(t, time.Now().Add(5*time.Minute), sweeper.deadline, 30*time.Second)
}

// TestEnrichmentReconcileWorkerReturnsSweepFailures keeps the failure visible to River. The next
// tick is what makes the system converge, not this retry -- but a sweep that swallowed its error
// would report success while coverage silently stopped.
func TestEnrichmentReconcileWorkerReturnsSweepFailures(t *testing.T) {
	sentinel := errors.New("pending query failed")
	worker := workers.NewEnrichmentReconcileWorker(&stubSweeper{err: sentinel}, nil)

	err := worker.Work(context.Background(), &river.Job[service.EnrichmentReconcileArgs]{})

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel, "the cause survives the wrap so River and the logs agree on why")
}

// recordingReconcileMetrics captures what the worker reported.
type recordingReconcileMetrics struct {
	sweeps   []string
	duration []time.Duration
	enqueued map[string]int
}

func (r *recordingReconcileMetrics) RecordSweep(_ context.Context, outcome string, d time.Duration) {
	r.sweeps = append(r.sweeps, outcome)
	r.duration = append(r.duration, d)
}

func (r *recordingReconcileMetrics) RecordEnqueued(_ context.Context, enrichment string, count int) {
	if r.enqueued == nil {
		r.enqueued = map[string]int{}
	}

	r.enqueued[enrichment] += count
}

func (r *recordingReconcileMetrics) RecordRetry(context.Context, string, string) {}

// TestEnrichmentReconcileWorkerRecordsASuccessfulSweep pins the happy path: one sweep counted, a
// real duration observed, and the enqueue counts attributed per enrichment.
func TestEnrichmentReconcileWorkerRecordsASuccessfulSweep(t *testing.T) {
	const sweepTakes = 5 * time.Millisecond

	metrics := &recordingReconcileMetrics{}
	sweeper := &stubSweeper{
		result: service.ReconcileResult{Enqueued: map[string]int{"sentiment": 3, "translation": 2}},
		takes:  sweepTakes,
	}

	require.NoError(t, workers.NewEnrichmentReconcileWorker(sweeper, metrics).
		Work(context.Background(), &river.Job[service.EnrichmentReconcileArgs]{}))

	assert.Equal(t, []string{observability.OutcomeSuccess}, metrics.sweeps)
	assert.Equal(t, map[string]int{"sentiment": 3, "translation": 2}, metrics.enqueued)
	require.Len(t, metrics.duration, 1)
	assert.GreaterOrEqual(t, metrics.duration[0], sweepTakes,
		"the recorded duration must cover the sweep itself, not just the bookkeeping around it")
}

// TestEnrichmentReconcileWorkerRecordsAFailedSweep is the half that is easy to get wrong: a sweep
// that failed part-way still enqueued whatever it enqueued, and a failing sweep is exactly when
// somebody needs its duration. Recording only on the success path would leave the error case --
// the one worth alerting on -- invisible.
func TestEnrichmentReconcileWorkerRecordsAFailedSweep(t *testing.T) {
	metrics := &recordingReconcileMetrics{}
	sweeper := &stubSweeper{
		result: service.ReconcileResult{Enqueued: map[string]int{"emotions": 4}},
		err:    errors.New("one enrichment failed"),
	}

	require.Error(t, workers.NewEnrichmentReconcileWorker(sweeper, metrics).
		Work(context.Background(), &river.Job[service.EnrichmentReconcileArgs]{}))

	assert.Equal(t, []string{observability.OutcomeError}, metrics.sweeps)
	assert.Equal(t, map[string]int{"emotions": 4}, metrics.enqueued,
		"work done before the failure is still work done, and still provider spend")
	require.Len(t, metrics.duration, 1)
}

// TestEnrichmentReconcileWorkerToleratesDisabledMetrics: metrics are optional and nil is the
// documented "disabled" value, so the sweep must not depend on them.
func TestEnrichmentReconcileWorkerToleratesDisabledMetrics(t *testing.T) {
	sweeper := &stubSweeper{result: service.ReconcileResult{Enqueued: map[string]int{"sentiment": 1}}}

	assert.NotPanics(t, func() {
		_ = workers.NewEnrichmentReconcileWorker(sweeper, nil).
			Work(context.Background(), &river.Job[service.EnrichmentReconcileArgs]{})
	})
}
