package workers

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

type recordingPurgeService struct {
	tenantIDs []string
	err       error
	// counts is returned alongside err: the repository reports what it committed before failing.
	counts *models.FeedbackRecordsPurgeCounts
}

func (r *recordingPurgeService) Purge(
	_ context.Context, tenantID string,
) (*models.FeedbackRecordsPurgeCounts, error) {
	r.tenantIDs = append(r.tenantIDs, tenantID)

	if r.err != nil {
		// Mirrors the service: partial progress is returned with the error, not dropped.
		return r.counts, r.err
	}

	return &models.FeedbackRecordsPurgeCounts{DeletedFeedbackRecords: 3}, nil
}

// purgeJob builds a job the way River delivers one. The embedded JobRow must be populated: River
// always sets it, and the worker reads Attempt/MaxAttempts from it to tell a retryable failure from
// the last one.
func purgeJob(tenantID string) *river.Job[service.FeedbackRecordsPurgeArgs] {
	return purgeJobAttempt(tenantID, 1, service.FeedbackRecordsPurgeMaxAttempts)
}

func purgeJobAttempt(tenantID string, attempt, maxAttempts int) *river.Job[service.FeedbackRecordsPurgeArgs] {
	return &river.Job[service.FeedbackRecordsPurgeArgs]{
		JobRow: &rivertype.JobRow{
			ID:          1,
			Attempt:     attempt,
			MaxAttempts: maxAttempts,
		},
		Args: service.FeedbackRecordsPurgeArgs{TenantID: tenantID},
	}
}

// capturePurgeLogs swaps the default logger for the duration of a test and returns the records it
// collected.
func capturePurgeLogs(t *testing.T) *purgeLogCapture {
	t.Helper()

	capture := &purgeLogCapture{}
	previous := slog.Default()

	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })

	return capture
}

type purgeLogCapture struct {
	records []slog.Record
}

func (c *purgeLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *purgeLogCapture) WithAttrs([]slog.Attr) slog.Handler       { return c }
func (c *purgeLogCapture) WithGroup(string) slog.Handler            { return c }

func (c *purgeLogCapture) Handle(_ context.Context, record slog.Record) error {
	c.records = append(c.records, record)

	return nil
}

func (c *purgeLogCapture) attr(t *testing.T, index int, key string) any {
	t.Helper()

	var found any

	c.records[index].Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = a.Value.Any()

			return false
		}

		return true
	})

	return found
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

	// A failed purge used to leave no trace: the worker logged only on success, and River reports
	// retries and final discards at INFO while hub-worker's fallback logger sits at WARN. The purge
	// commits in batches, so a silent failure leaves the dataset partly emptied with nothing to
	// explain it.
	t.Run("logs a retryable failure with the progress it committed", func(t *testing.T) {
		logs := capturePurgeLogs(t)
		svc := &recordingPurgeService{
			err:    errors.New("purge failed"),
			counts: &models.FeedbackRecordsPurgeCounts{DeletedFeedbackRecords: 1200, DeletedEmbeddings: 900},
		}

		err := NewFeedbackRecordsPurgeWorker(svc).Work(context.Background(), purgeJobAttempt("org-1", 2, 5))

		require.Error(t, err)
		require.Len(t, logs.records, 1)
		assert.Equal(t, slog.LevelWarn, logs.records[0].Level,
			"an attempt with retries left is not yet a permanent failure")
		assert.Equal(t, "org-1", logs.attr(t, 0, "tenant_id"))
		assert.Equal(t, int64(1200), logs.attr(t, 0, "deleted_feedback_records"),
			"the committed progress must be reported, not discarded")
	})

	// The last attempt is the one an operator has to act on: nothing will retry it, so the dataset
	// stays partly emptied until someone requests another purge.
	t.Run("logs the last attempt at error level", func(t *testing.T) {
		logs := capturePurgeLogs(t)
		svc := &recordingPurgeService{err: errors.New("purge failed")}

		err := NewFeedbackRecordsPurgeWorker(svc).Work(context.Background(), purgeJobAttempt("org-1", 5, 5))

		require.Error(t, err)
		require.Len(t, logs.records, 1)
		assert.Equal(t, slog.LevelError, logs.records[0].Level)
	})

	// A purge is unbounded, so it needs a timeout well above River's default. Zero would mean "use
	// the default", which is how a large purge would get killed and retried forever.
	t.Run("allows a long single attempt", func(t *testing.T) {
		timeout := NewFeedbackRecordsPurgeWorker(&recordingPurgeService{}).Timeout(purgeJob("org-1"))

		assert.Positive(t, timeout)
		assert.Equal(t, feedbackRecordsPurgeTimeout, timeout)
	})
}
