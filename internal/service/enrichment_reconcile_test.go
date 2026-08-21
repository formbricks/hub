package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
)

type fakeReconcileRepo struct {
	depths     map[string]int64
	pending    map[string][]repository.PendingEnrichmentTarget
	limits     map[string]int
	listErr    map[string]error
	depthsErr  error
	depthCalls int
}

func (f *fakeReconcileRepo) CountRunnableByQueue(_ context.Context, _ []string) (map[string]int64, error) {
	f.depthCalls++

	if f.depthsErr != nil {
		return nil, f.depthsErr
	}

	return f.depths, nil
}

func (f *fakeReconcileRepo) ListPendingEnrichment(
	_ context.Context, enrichment, _ string, limit int,
) ([]repository.PendingEnrichmentTarget, error) {
	if f.limits == nil {
		f.limits = map[string]int{}
	}

	f.limits[enrichment] = limit

	if err := f.listErr[enrichment]; err != nil {
		return nil, err
	}

	targets := f.pending[enrichment]
	if len(targets) > limit {
		targets = targets[:limit]
	}

	return targets, nil
}

type fakeBatchInserter struct {
	params    []river.InsertManyParams
	duplicate int
	err       error
}

func (f *fakeBatchInserter) InsertMany(
	_ context.Context, params []river.InsertManyParams,
) ([]*rivertype.JobInsertResult, error) {
	if f.err != nil {
		return nil, f.err
	}

	f.params = append(f.params, params...)

	results := make([]*rivertype.JobInsertResult, 0, len(params))
	for i := range params {
		results = append(results, &rivertype.JobInsertResult{UniqueSkippedAsDuplicate: i < f.duplicate})
	}

	return results, nil
}

func targets(n int) []repository.PendingEnrichmentTarget {
	out := make([]repository.PendingEnrichmentTarget, 0, n)
	for range n {
		out = append(out, repository.PendingEnrichmentTarget{ID: uuid.Must(uuid.NewV7()), TenantID: "t"})
	}

	return out
}

func newSweepService(repo *fakeReconcileRepo, inserter *fakeBatchInserter, depth int) *EnrichmentReconcileService {
	svc := NewEnrichmentReconcileService(NewEnrichmentReconcileServiceParams{
		Repo:        repo,
		TargetDepth: depth,
		Enabled:     []string{models.EnrichmentNameSentiment},
		MaxAttempts: map[string]int{models.EnrichmentNameSentiment: 3},
	})
	svc.SetInserter(inserter)

	return svc
}

// TestSweepTopsUpToTargetDepth is the arithmetic the whole rate-control story rests on: a tick adds
// only what the workers have already drained, so the queue converges on the target instead of
// growing without bound.
func TestSweepTopsUpToTargetDepth(t *testing.T) {
	t.Run("asks for exactly the remaining room", func(t *testing.T) {
		repo := &fakeReconcileRepo{
			depths:  map[string]int64{SentimentsBackfillQueueName: 400},
			pending: map[string][]repository.PendingEnrichmentTarget{models.EnrichmentNameSentiment: targets(1000)},
		}
		inserter := &fakeBatchInserter{}

		result, err := newSweepService(repo, inserter, 1000).Sweep(context.Background())
		require.NoError(t, err)

		assert.Equal(t, 600, repo.limits[models.EnrichmentNameSentiment], "1000 target minus 400 already runnable")
		assert.Equal(t, 600, result.Enqueued[models.EnrichmentNameSentiment])
		assert.Len(t, inserter.params, 600)
	})

	t.Run("a full queue is skipped, not topped up", func(t *testing.T) {
		repo := &fakeReconcileRepo{
			depths:  map[string]int64{SentimentsBackfillQueueName: 1000},
			pending: map[string][]repository.PendingEnrichmentTarget{models.EnrichmentNameSentiment: targets(50)},
		}
		inserter := &fakeBatchInserter{}

		result, err := newSweepService(repo, inserter, 1000).Sweep(context.Background())
		require.NoError(t, err)

		assert.Empty(t, inserter.params, "nothing enqueued while the queue is at depth")
		assert.Contains(t, result.Skipped, models.EnrichmentNameSentiment)
		assert.NotContains(t, repo.limits, models.EnrichmentNameSentiment, "and the pending set is not even queried")
	})

	// Over-depth happens: the event path inserts onto the live queue, but a previous sweep plus a
	// lowered target can leave the backfill queue above it. Subtracting past zero would ask for a
	// negative limit, which Postgres rejects.
	t.Run("a queue past the target does not ask for a negative limit", func(t *testing.T) {
		repo := &fakeReconcileRepo{
			depths:  map[string]int64{SentimentsBackfillQueueName: 5000},
			pending: map[string][]repository.PendingEnrichmentTarget{models.EnrichmentNameSentiment: targets(50)},
		}
		inserter := &fakeBatchInserter{}

		_, err := newSweepService(repo, inserter, 1000).Sweep(context.Background())
		require.NoError(t, err)
		assert.Empty(t, inserter.params)
	})
}

// TestSweepEnqueuesTheRightShape pins what a reconciled job looks like: the same args the event
// path uses, on the backfill lane, unique across the in-flight states.
func TestSweepEnqueuesTheRightShape(t *testing.T) {
	repo := &fakeReconcileRepo{
		pending: map[string][]repository.PendingEnrichmentTarget{models.EnrichmentNameSentiment: targets(1)},
	}
	inserter := &fakeBatchInserter{}

	_, err := newSweepService(repo, inserter, 10).Sweep(context.Background())
	require.NoError(t, err)
	require.Len(t, inserter.params, 1)

	opts := inserter.params[0].InsertOpts
	assert.Equal(t, SentimentsBackfillQueueName, opts.Queue, "reconciled work never lands on the live queue")
	assert.Equal(t, 3, opts.MaxAttempts, "a reconciled job is retried like an event-driven one")
	assert.True(t, opts.UniqueOpts.ByArgs)
	assert.Equal(t, InFlightUniqueStates(), opts.UniqueOpts.ByState)
	assert.NotContains(t, opts.UniqueOpts.ByState, rivertype.JobStateCompleted,
		"completed in the set means the first sweep of a record is the only one that ever runs")
	assert.Contains(t, opts.UniqueOpts.ByState, rivertype.JobStateRetryable,
		"a job waiting out its backoff must not be enqueued a second time")

	_, isSentiment := inserter.params[0].Args.(FeedbackSentimentArgs)
	assert.True(t, isSentiment, "args = %T, want the same type the event path inserts", inserter.params[0].Args)
}

// TestSweepCountsNewWorkOnly guards the number an operator reads: River skipping a duplicate is not
// progress, and counting it would make an idle sweep look busy.
func TestSweepCountsNewWorkOnly(t *testing.T) {
	repo := &fakeReconcileRepo{
		pending: map[string][]repository.PendingEnrichmentTarget{models.EnrichmentNameSentiment: targets(10)},
	}
	inserter := &fakeBatchInserter{duplicate: 4}

	result, err := newSweepService(repo, inserter, 100).Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 6, result.Enqueued[models.EnrichmentNameSentiment], "10 attempted, 4 already queued")
}

// TestSweepIsIndependentPerEnrichment: the backlogs are unrelated, so one failing query must not
// leave the others un-swept. A provider outage tends to strand work in more than one pipeline at
// once, which is exactly when abandoning the rest would hurt.
func TestSweepIsIndependentPerEnrichment(t *testing.T) {
	repo := &fakeReconcileRepo{
		pending: map[string][]repository.PendingEnrichmentTarget{
			models.EnrichmentNameSentiment: targets(3),
			models.EnrichmentNameEmotions:  targets(5),
		},
		listErr: map[string]error{models.EnrichmentNameSentiment: errors.New("query blew up")},
	}
	inserter := &fakeBatchInserter{}

	svc := NewEnrichmentReconcileService(NewEnrichmentReconcileServiceParams{
		Repo:        repo,
		TargetDepth: 100,
		Enabled:     []string{models.EnrichmentNameSentiment, models.EnrichmentNameEmotions},
		MaxAttempts: map[string]int{},
	})
	svc.SetInserter(inserter)

	result, err := svc.Sweep(context.Background())
	require.Error(t, err, "the failure is still reported")
	assert.Equal(t, 5, result.Enqueued[models.EnrichmentNameEmotions], "emotions was swept regardless")
}

// TestSweepRefusesWithoutAnInserter: the wiring builds this service before the River client exists,
// so a missed SetInserter is a live possibility. Reporting a successful zero would hide it forever.
func TestSweepRefusesWithoutAnInserter(t *testing.T) {
	svc := NewEnrichmentReconcileService(NewEnrichmentReconcileServiceParams{
		Repo:    &fakeReconcileRepo{},
		Enabled: []string{models.EnrichmentNameSentiment},
	})

	_, err := svc.Sweep(context.Background())
	require.ErrorIs(t, err, ErrReconcileInserterUnset)
}

// TestSweepWithNothingEnabledDoesNotQuery: a deployment with no enrichment provider has nothing to
// sweep for, and must not pay for a queue-depth query every tick to discover that.
func TestSweepWithNothingEnabledDoesNotQuery(t *testing.T) {
	repo := &fakeReconcileRepo{}
	svc := newSweepService(repo, &fakeBatchInserter{}, 100)
	svc.enabled = nil

	result, err := svc.Sweep(context.Background())
	require.NoError(t, err)
	assert.Empty(t, result.Enqueued)
	assert.Zero(t, repo.depthCalls)
}
