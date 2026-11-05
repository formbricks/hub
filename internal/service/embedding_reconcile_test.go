package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
)

type stubEmbeddingReconcileRepository struct {
	depth       int
	depthErr    error
	ids         []uuid.UUID
	listErr     error
	listModel   string
	listLimit   int
	retryBefore time.Time
	depthQueue  string
	idsByModel  map[string][]uuid.UUID
	listCalls   []listCall
}

func (r *stubEmbeddingReconcileRepository) ListPendingEmbeddingIDs(
	_ context.Context, model string, inputKind models.EmbeddingInputKind, retryBefore time.Time, limit int,
) ([]uuid.UUID, error) {
	r.listModel = model
	r.listLimit = limit
	r.retryBefore = retryBefore
	r.listCalls = append(r.listCalls, listCall{model: model, inputKind: inputKind, limit: limit})

	if r.listErr != nil {
		return nil, r.listErr
	}

	ids := r.ids
	if byModel, ok := r.idsByModel[model]; ok {
		ids = byModel
	}

	if len(ids) > limit {
		return ids[:limit], nil
	}

	return ids, nil
}

type listCall struct {
	model     string
	inputKind models.EmbeddingInputKind
	limit     int
}

// taxonomyOnly is the single-target configuration the original sweep tests were written against.
var taxonomyOnly = []EmbeddingReconcileTarget{
	{Model: "taxonomy:model", InputKind: models.EmbeddingInputKindTaxonomyTranslated},
}

func (r *stubEmbeddingReconcileRepository) CountRunnableEmbeddingJobs(
	_ context.Context, queue string,
) (int, error) {
	r.depthQueue = queue

	return r.depth, r.depthErr
}

type recordingBatchInserter struct {
	params  []river.InsertManyParams
	results []*rivertype.JobInsertResult
	err     error
}

func (i *recordingBatchInserter) InsertMany(
	_ context.Context, params []river.InsertManyParams,
) ([]*rivertype.JobInsertResult, error) {
	i.params = params

	if i.err != nil {
		return nil, i.err
	}

	if i.results != nil {
		return i.results, nil
	}

	results := make([]*rivertype.JobInsertResult, len(params))
	for index := range results {
		results[index] = &rivertype.JobInsertResult{}
	}

	return results, nil
}

func TestEmbeddingReconcileSweepTopsUpBoundedRepairQueue(t *testing.T) {
	ids := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	repo := &stubEmbeddingReconcileRepository{depth: 98, ids: ids}
	inserter := &recordingBatchInserter{}
	retryAfter := 15 * time.Minute
	reconciler := NewEmbeddingReconcileService(repo, taxonomyOnly, 100, 5, retryAfter)
	reconciler.SetInserter(inserter)

	started := time.Now()
	result, err := reconciler.Sweep(context.Background())
	require.NoError(t, err)
	require.Equal(t, EmbeddingsReconcileQueueName, repo.depthQueue)
	require.Equal(t, "taxonomy:model", repo.listModel)
	require.Equal(t, 2, repo.listLimit, "only the available target-depth room may be queried")
	assert.WithinDuration(t, started.Add(-retryAfter), repo.retryBefore, time.Second)
	require.Len(t, inserter.params, 2)
	assert.Equal(t, EmbeddingReconcileResult{Found: 2, Enqueued: 2, Depth: 98}, result)

	for index, param := range inserter.params {
		args, ok := param.Args.(FeedbackEmbeddingArgs)
		require.True(t, ok)
		assert.Equal(t, ids[index], args.FeedbackRecordID)
		assert.Equal(t, "taxonomy:model", args.Model)
		assert.Equal(t, models.EmbeddingInputKindTaxonomyTranslated, args.InputKind)
		assert.Equal(t, ValueTextHashRepair, args.ValueTextHash)
		require.NotNil(t, param.InsertOpts)
		assert.Equal(t, EmbeddingsReconcileQueueName, param.InsertOpts.Queue)
		assert.Equal(t, 4, param.InsertOpts.Priority)
		assert.Equal(t, 5, param.InsertOpts.MaxAttempts)
		assert.True(t, param.InsertOpts.UniqueOpts.ByArgs)
		assert.ElementsMatch(t, []rivertype.JobState{
			rivertype.JobStateAvailable,
			rivertype.JobStatePending,
			rivertype.JobStateRetryable,
			rivertype.JobStateRunning,
			rivertype.JobStateScheduled,
		}, param.InsertOpts.UniqueOpts.ByState)
	}
}

func TestEmbeddingReconcileSweepStopsAtTarget(t *testing.T) {
	repo := &stubEmbeddingReconcileRepository{depth: 100}
	inserter := &recordingBatchInserter{}
	reconciler := NewEmbeddingReconcileService(repo, taxonomyOnly, 100, 5, 15*time.Minute)
	reconciler.SetInserter(inserter)

	result, err := reconciler.Sweep(context.Background())
	require.NoError(t, err)
	assert.True(t, result.AtTarget)
	assert.Equal(t, 100, result.Depth)
	assert.Zero(t, repo.listLimit, "a full lane must not scan the record backlog")
	assert.Empty(t, inserter.params)
}

func TestEmbeddingReconcileSweepCountsUniqueSkipsTruthfully(t *testing.T) {
	repo := &stubEmbeddingReconcileRepository{
		ids: []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())},
	}
	inserter := &recordingBatchInserter{results: []*rivertype.JobInsertResult{
		{},
		{UniqueSkippedAsDuplicate: true},
	}}
	reconciler := NewEmbeddingReconcileService(repo, taxonomyOnly, 2, 5, 15*time.Minute)
	reconciler.SetInserter(inserter)

	result, err := reconciler.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, result.Found)
	assert.Equal(t, 1, result.Enqueued)
}

func TestEmbeddingReconcileSweepPropagatesBoundedFailures(t *testing.T) {
	t.Run("inserter is required", func(t *testing.T) {
		reconciler := NewEmbeddingReconcileService(
			&stubEmbeddingReconcileRepository{}, taxonomyOnly, 100, 5, 15*time.Minute)
		_, err := reconciler.Sweep(context.Background())
		require.ErrorIs(t, err, ErrEmbeddingReconcileInserterUnset)
	})

	t.Run("depth read", func(t *testing.T) {
		reconciler := NewEmbeddingReconcileService(
			&stubEmbeddingReconcileRepository{depthErr: errors.New("db unavailable")},
			taxonomyOnly, 100, 5, 15*time.Minute)
		reconciler.SetInserter(&recordingBatchInserter{})
		_, err := reconciler.Sweep(context.Background())
		require.ErrorContains(t, err, "read repair queue depth")
	})

	t.Run("record scan", func(t *testing.T) {
		reconciler := NewEmbeddingReconcileService(
			&stubEmbeddingReconcileRepository{listErr: errors.New("db unavailable")},
			taxonomyOnly, 100, 5, 15*time.Minute)
		reconciler.SetInserter(&recordingBatchInserter{})
		_, err := reconciler.Sweep(context.Background())
		require.ErrorContains(t, err, "list pending embeddings")
	})

	t.Run("batch insert", func(t *testing.T) {
		reconciler := NewEmbeddingReconcileService(
			&stubEmbeddingReconcileRepository{ids: []uuid.UUID{uuid.Must(uuid.NewV7())}},
			taxonomyOnly, 100, 5, 15*time.Minute)
		reconciler.SetInserter(&recordingBatchInserter{err: errors.New("river unavailable")})
		_, err := reconciler.Sweep(context.Background())
		require.ErrorContains(t, err, "enqueue embedding repairs")
	})
}

// TestEmbeddingReconcileSweepSharesRoomAcrossTargets keeps a big backlog on one embedding from
// starving repair of the other, and keeps the lane full whenever any target has work: targets with
// backlog split the room evenly, and whatever one cannot use goes to the others, whichever of them
// is served first.
func TestEmbeddingReconcileSweepSharesRoomAcrossTargets(t *testing.T) {
	newIDs := func(n int) []uuid.UUID {
		ids := make([]uuid.UUID, n)
		for i := range ids {
			ids[i] = uuid.Must(uuid.NewV7())
		}

		return ids
	}

	targets := []EmbeddingReconcileTarget{
		{Model: "raw", InputKind: models.EmbeddingInputKindRaw},
		{Model: "taxonomy:model", InputKind: models.EmbeddingInputKindTaxonomyTranslated},
	}

	enqueuedByKind := func(t *testing.T, params []river.InsertManyParams) map[models.EmbeddingInputKind]int {
		t.Helper()

		kinds := map[models.EmbeddingInputKind]int{}

		for _, param := range params {
			args, ok := param.Args.(FeedbackEmbeddingArgs)
			require.True(t, ok)

			kinds[args.InputKind]++
		}

		return kinds
	}

	t.Run("both backlogged: half each", func(t *testing.T) {
		repo := &stubEmbeddingReconcileRepository{idsByModel: map[string][]uuid.UUID{
			"raw": newIDs(500), "taxonomy:model": newIDs(500),
		}}
		inserter := &recordingBatchInserter{}
		reconciler := NewEmbeddingReconcileService(repo, targets, 100, 5, 15*time.Minute)
		reconciler.SetInserter(inserter)

		result, err := reconciler.Sweep(context.Background())
		require.NoError(t, err)
		assert.Equal(t, []listCall{
			{model: "raw", inputKind: models.EmbeddingInputKindRaw, limit: 100},
			{model: "taxonomy:model", inputKind: models.EmbeddingInputKindTaxonomyTranslated, limit: 100},
		}, repo.listCalls, "each target is asked for the whole room, once")
		assert.Equal(t, 100, result.Enqueued)
		assert.Equal(t, map[models.EmbeddingInputKind]int{
			models.EmbeddingInputKindRaw:                50,
			models.EmbeddingInputKindTaxonomyTranslated: 50,
		}, enqueuedByKind(t, inserter.params))
	})

	t.Run("a single free slot alternates between backlogged targets", func(t *testing.T) {
		repo := &stubEmbeddingReconcileRepository{depth: 99, idsByModel: map[string][]uuid.UUID{
			"raw": newIDs(5), "taxonomy:model": newIDs(5),
		}}
		inserter := &recordingBatchInserter{}
		reconciler := NewEmbeddingReconcileService(repo, targets, 100, 5, 15*time.Minute)
		reconciler.SetInserter(inserter)

		served := map[models.EmbeddingInputKind]int{}

		for range 2 {
			_, err := reconciler.Sweep(context.Background())
			require.NoError(t, err)
			require.Len(t, inserter.params, 1)

			for kind, n := range enqueuedByKind(t, inserter.params) {
				served[kind] += n
			}
		}

		assert.Equal(t, map[models.EmbeddingInputKind]int{
			models.EmbeddingInputKindRaw:                1,
			models.EmbeddingInputKindTaxonomyTranslated: 1,
		}, served)
	})

	// Regression for the first pass of the fair share, which capped the first target at half the
	// room and let the room the second could not use go to waste: 50 of 100 slots filled when only
	// search had backlog and was served first, and none at all every other sweep with one free slot.
	t.Run("one backlogged target gets the whole room whichever is served first", func(t *testing.T) {
		for _, backlogged := range targets {
			repo := &stubEmbeddingReconcileRepository{idsByModel: map[string][]uuid.UUID{
				"raw": {}, "taxonomy:model": {}, backlogged.Model: newIDs(500),
			}}
			inserter := &recordingBatchInserter{}
			reconciler := NewEmbeddingReconcileService(repo, targets, 100, 5, 15*time.Minute)
			reconciler.SetInserter(inserter)

			for sweep := range targets {
				result, err := reconciler.Sweep(context.Background())
				require.NoError(t, err)
				assert.Equal(t, 100, result.Enqueued, "%s backlogged, sweep %d", backlogged.Model, sweep)
				assert.Equal(t, map[models.EmbeddingInputKind]int{backlogged.InputKind: 100},
					enqueuedByKind(t, inserter.params))
			}

			repo.depth = 99

			for sweep := range targets {
				result, err := reconciler.Sweep(context.Background())
				require.NoError(t, err)
				assert.Equal(t, 1, result.Enqueued, "%s backlogged, one free slot, sweep %d", backlogged.Model, sweep)
			}
		}
	})

	t.Run("unused share flows to the other target whichever is served first", func(t *testing.T) {
		repo := &stubEmbeddingReconcileRepository{idsByModel: map[string][]uuid.UUID{
			"raw": newIDs(10), "taxonomy:model": newIDs(500),
		}}
		inserter := &recordingBatchInserter{}
		reconciler := NewEmbeddingReconcileService(repo, targets, 100, 5, 15*time.Minute)
		reconciler.SetInserter(inserter)

		for sweep := range targets {
			result, err := reconciler.Sweep(context.Background())
			require.NoError(t, err)
			assert.Equal(t, 100, result.Enqueued, "sweep %d", sweep)
			assert.Equal(t, map[models.EmbeddingInputKind]int{
				models.EmbeddingInputKindRaw:                10,
				models.EmbeddingInputKindTaxonomyTranslated: 90,
			}, enqueuedByKind(t, inserter.params), "sweep %d", sweep)
		}
	})
}

// TestShareRepairRoom pins the max-min fair split: equal shares among targets with candidates,
// what one cannot use going to the rest, a remainder going one slot each in order, and never more
// than room in total or more than a target has.
func TestShareRepairRoom(t *testing.T) {
	ids := func(n int) []uuid.UUID { return make([]uuid.UUID, n) }

	for _, testCase := range []struct {
		name       string
		candidates [][]uuid.UUID
		room       int
		want       []int
	}{
		{"both backlogged", [][]uuid.UUID{ids(100), ids(100)}, 100, []int{50, 50}},
		{"first only", [][]uuid.UUID{ids(100), ids(0)}, 100, []int{100, 0}},
		{"second only", [][]uuid.UUID{ids(0), ids(100)}, 100, []int{0, 100}},
		{"short first", [][]uuid.UUID{ids(10), ids(100)}, 100, []int{10, 90}},
		{"short second", [][]uuid.UUID{ids(100), ids(10)}, 100, []int{90, 10}},
		{"odd room, remainder to first", [][]uuid.UUID{ids(100), ids(100)}, 3, []int{2, 1}},
		{"one slot, first served", [][]uuid.UUID{ids(5), ids(5)}, 1, []int{1, 0}},
		{"one slot, first empty", [][]uuid.UUID{ids(0), ids(5)}, 1, []int{0, 1}},
		{"less work than room", [][]uuid.UUID{ids(3), ids(4)}, 100, []int{3, 4}},
		{"no work", [][]uuid.UUID{ids(0), ids(0)}, 100, []int{0, 0}},
		{"no room", [][]uuid.UUID{ids(5), ids(5)}, 0, []int{0, 0}},
		{"more candidates than room", [][]uuid.UUID{ids(200)}, 100, []int{100}},
		{"three targets", [][]uuid.UUID{ids(5), ids(100), ids(100)}, 100, []int{5, 48, 47}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, shareRepairRoom(testCase.candidates, testCase.room))
		})
	}
}
