package workers

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

type recordingPurgeService struct {
	tenantIDs []string
	err       error
}

func (r *recordingPurgeService) Purge(
	_ context.Context, tenantID string,
) (*models.FeedbackRecordsPurgeCounts, error) {
	r.tenantIDs = append(r.tenantIDs, tenantID)

	if r.err != nil {
		return nil, r.err
	}

	return &models.FeedbackRecordsPurgeCounts{DeletedFeedbackRecords: 3}, nil
}

func purgeJob(tenantID string) *river.Job[service.FeedbackRecordsPurgeArgs] {
	return &river.Job[service.FeedbackRecordsPurgeArgs]{
		Args: service.FeedbackRecordsPurgeArgs{TenantID: tenantID},
	}
}

func TestFeedbackRecordsPurgeWorker_Work(t *testing.T) {
	t.Run("purges the tenant carried on the job", func(t *testing.T) {
		svc := &recordingPurgeService{}

		require.NoError(t, NewFeedbackRecordsPurgeWorker(svc).Work(context.Background(), purgeJob("org-1")))
		assert.Equal(t, []string{"org-1"}, svc.tenantIDs)
	})

	// The error must propagate so River retries. Swallowing it would mark the job complete with the
	// tenant's records still in place, and nothing would ever come back for them.
	t.Run("returns the failure so River retries", func(t *testing.T) {
		purgeErr := errors.New("purge failed")
		svc := &recordingPurgeService{err: purgeErr}

		err := NewFeedbackRecordsPurgeWorker(svc).Work(context.Background(), purgeJob("org-1"))

		require.ErrorIs(t, err, purgeErr)
		// The tenant is not interpolated into the error; it is already on the job row.
		assert.NotContains(t, err.Error(), "org-1")
	})

	// A purge is unbounded, so it needs a timeout well above River's default. Zero would mean "use
	// the default", which is how a large purge would get killed and retried forever.
	t.Run("allows a long single attempt", func(t *testing.T) {
		timeout := NewFeedbackRecordsPurgeWorker(&recordingPurgeService{}).Timeout(purgeJob("org-1"))

		assert.Positive(t, timeout)
		assert.Equal(t, feedbackRecordsPurgeTimeout, timeout)
	})
}
