package service

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type stubPurgeRepo struct {
	counts    *models.FeedbackRecordsPurgeCounts
	err       error
	tenantIDs []string
}

func (s *stubPurgeRepo) PurgeFeedbackRecordsByTenant(
	_ context.Context, tenantID string,
) (*models.FeedbackRecordsPurgeCounts, error) {
	s.tenantIDs = append(s.tenantIDs, tenantID)

	if s.err != nil {
		return nil, s.err
	}

	if s.counts != nil {
		return s.counts, nil
	}

	return &models.FeedbackRecordsPurgeCounts{}, nil
}

func TestFeedbackRecordsPurgeService_Enqueue(t *testing.T) {
	t.Run("enqueues one purge job on the purge queue", func(t *testing.T) {
		inserter := &recordingInserter{}

		accepted, err := NewFeedbackRecordsPurgeService(&stubPurgeRepo{}, inserter).
			Enqueue(context.Background(), "org-1")
		require.NoError(t, err)

		assert.Equal(t, "org-1", accepted.TenantID)
		assert.Equal(t, models.FeedbackRecordsPurgeStatusAccepted, accepted.Status)

		require.Len(t, inserter.args, 1)
		args, ok := inserter.args[0].(FeedbackRecordsPurgeArgs)
		require.True(t, ok, "enqueued args type = %T", inserter.args[0])
		assert.Equal(t, "org-1", args.TenantID)
		assert.Equal(t, FeedbackRecordsPurgeQueueName, inserter.opts[0].Queue)
		assert.Equal(t, FeedbackRecordsPurgeMaxAttempts, inserter.opts[0].MaxAttempts)
	})

	// Regression test for the bug this shipped with: relying on River's DEFAULT unique states
	// includes `completed`, and with no ByPeriod the window is unbounded — so the first purge of a
	// tenant was the only one that ever ran, every later request being skipped as a duplicate while
	// still returning 202. ByState must be set, and must not contain `completed`.
	//
	// tests/feedback_records_purge_test.go proves the resulting behaviour against a real River
	// queue; this pins the option itself so the intent is readable at the enqueue site.
	t.Run("dedupes by tenant across in-flight states only", func(t *testing.T) {
		inserter := &recordingInserter{}

		_, err := NewFeedbackRecordsPurgeService(&stubPurgeRepo{}, inserter).
			Enqueue(context.Background(), "org-1")
		require.NoError(t, err)

		unique := inserter.opts[0].UniqueOpts
		assert.True(t, unique.ByArgs, "purges must dedupe by tenant")
		assert.NotEmpty(t, unique.ByState,
			"River's default state set includes completed, which would swallow every later purge")
		assert.NotContains(t, unique.ByState, rivertype.JobStateCompleted,
			"a completed purge must never block the next one")
		// The four states River requires, plus retryable so a request during backoff collapses into
		// the existing purge rather than inserting a second one that fights it for the tenant lock.
		assert.ElementsMatch(t, []rivertype.JobState{
			rivertype.JobStateAvailable,
			rivertype.JobStatePending,
			rivertype.JobStateRetryable,
			rivertype.JobStateRunning,
			rivertype.JobStateScheduled,
		}, unique.ByState)
	})

	t.Run("normalizes the tenant before enqueueing", func(t *testing.T) {
		inserter := &recordingInserter{}

		accepted, err := NewFeedbackRecordsPurgeService(&stubPurgeRepo{}, inserter).
			Enqueue(context.Background(), "  org-1  ")
		require.NoError(t, err)

		assert.Equal(t, "org-1", accepted.TenantID)

		args, ok := inserter.args[0].(FeedbackRecordsPurgeArgs)
		require.True(t, ok)
		// An untrimmed tenant would match no rows on the Hub side, so the purge would silently
		// delete nothing while still reporting accepted.
		assert.Equal(t, "org-1", args.TenantID)
	})

	t.Run("rejects a blank tenant without enqueueing", func(t *testing.T) {
		inserter := &recordingInserter{}

		_, err := NewFeedbackRecordsPurgeService(&stubPurgeRepo{}, inserter).
			Enqueue(context.Background(), "   ")

		var validationErr *huberrors.ValidationError
		require.ErrorAs(t, err, &validationErr)
		assert.Empty(t, inserter.args, "a blank tenant must never reach the queue")
	})

	t.Run("reports a clear error when built without an inserter", func(t *testing.T) {
		_, err := NewFeedbackRecordsPurgeService(&stubPurgeRepo{}, nil).
			Enqueue(context.Background(), "org-1")

		require.ErrorIs(t, err, ErrPurgeInserterNotConfigured)
	})

	t.Run("propagates an insert failure instead of reporting accepted", func(t *testing.T) {
		insertErr := errors.New("queue down")
		inserter := &recordingInserter{err: insertErr}

		accepted, err := NewFeedbackRecordsPurgeService(&stubPurgeRepo{}, inserter).
			Enqueue(context.Background(), "org-1")

		require.ErrorIs(t, err, insertErr)
		assert.Nil(t, accepted)
	})
}

func TestFeedbackRecordsPurgeService_Purge(t *testing.T) {
	t.Run("purges the tenant and returns counts", func(t *testing.T) {
		repo := &stubPurgeRepo{counts: &models.FeedbackRecordsPurgeCounts{
			DeletedFeedbackRecords: 3,
			DeletedEmbeddings:      2,
			TenantTaxonomyDeleteCounts: models.TenantTaxonomyDeleteCounts{
				ClusterMemberships: 5,
				Runs:               1,
			},
		}}

		counts, err := NewFeedbackRecordsPurgeService(repo, nil).Purge(context.Background(), "org-1")
		require.NoError(t, err)

		assert.Equal(t, int64(3), counts.DeletedFeedbackRecords)
		assert.Equal(t, int64(2), counts.DeletedEmbeddings)
		assert.Equal(t, int64(5), counts.ClusterMemberships)
		assert.Equal(t, int64(1), counts.Runs)
		assert.Equal(t, []string{"org-1"}, repo.tenantIDs)
	})

	// Job args outlive the request that created them, so the worker path re-scopes rather than
	// trusting enqueue-time validation.
	t.Run("re-normalizes the tenant from the job args", func(t *testing.T) {
		repo := &stubPurgeRepo{}

		_, err := NewFeedbackRecordsPurgeService(repo, nil).Purge(context.Background(), "  org-1  ")
		require.NoError(t, err)

		assert.Equal(t, []string{"org-1"}, repo.tenantIDs)
	})

	t.Run("rejects a blank tenant without touching the repository", func(t *testing.T) {
		repo := &stubPurgeRepo{}

		_, err := NewFeedbackRecordsPurgeService(repo, nil).Purge(context.Background(), "")

		var validationErr *huberrors.ValidationError
		require.ErrorAs(t, err, &validationErr)
		assert.Empty(t, repo.tenantIDs, "a blank tenant must never reach an unscoped delete")
	})

	t.Run("propagates a tenant write conflict for River to retry", func(t *testing.T) {
		conflict := huberrors.NewTenantWriteConflictError("tenant-owned writes in progress; retry purge later")
		repo := &stubPurgeRepo{err: conflict}

		_, err := NewFeedbackRecordsPurgeService(repo, nil).Purge(context.Background(), "org-1")

		var conflictErr *huberrors.TenantWriteConflictError
		require.ErrorAs(t, err, &conflictErr)
	})
}

// Guards the queue the purge is inserted on against silently sharing an enrichment queue, where a
// long purge would stall live enrichment throughput.
func TestFeedbackRecordsPurgeQueueIsDedicated(t *testing.T) {
	for _, other := range []string{
		river.QueueDefault,
		EmbeddingsQueueName,
		TranslationsQueueName,
		TranslationBackfillsQueueName,
		SentimentsQueueName,
		EmotionsQueueName,
	} {
		assert.NotEqual(t, FeedbackRecordsPurgeQueueName, other)
	}
}
