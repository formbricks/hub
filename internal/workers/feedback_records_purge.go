package workers

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

// feedbackRecordsPurgeService is the minimal interface the worker needs.
type feedbackRecordsPurgeService interface {
	Purge(ctx context.Context, tenantID string) (*models.FeedbackRecordsPurgeCounts, error)
}

// FeedbackRecordsPurgeWorker deletes every feedback record for one tenant, plus the data derived
// from those records. It runs off the request path because the delete is unbounded and would
// otherwise outlive the API server's write timeout on a large tenant.
type FeedbackRecordsPurgeWorker struct {
	river.WorkerDefaults[service.FeedbackRecordsPurgeArgs]

	service feedbackRecordsPurgeService
}

// NewFeedbackRecordsPurgeWorker creates the worker.
func NewFeedbackRecordsPurgeWorker(svc feedbackRecordsPurgeService) *FeedbackRecordsPurgeWorker {
	return &FeedbackRecordsPurgeWorker{service: svc}
}

// feedbackRecordsPurgeMaxWorkers is the concurrency of the purge queue. See the queue declaration
// in wiring.go for why it is one.
const feedbackRecordsPurgeMaxWorkers = 1

// feedbackRecordsPurgeTimeout bounds a single purge attempt. River rescues a job that exceeds it,
// and the retry resumes rather than restarting: the purge commits in batches, so the records
// deleted before the timeout stay deleted and the next attempt continues from there. That is the
// property the batching exists for — a single-transaction purge would roll back on every rescue and
// a tenant too large for one attempt could never be purged at all.
const feedbackRecordsPurgeTimeout = 30 * time.Minute

// Timeout limits how long a single purge attempt can run.
func (w *FeedbackRecordsPurgeWorker) Timeout(*river.Job[service.FeedbackRecordsPurgeArgs]) time.Duration {
	return feedbackRecordsPurgeTimeout
}

// Work purges the tenant's feedback records. The service re-scopes the work to the tenant in the
// job args rather than trusting enqueue-time validation.
func (w *FeedbackRecordsPurgeWorker) Work(
	ctx context.Context, job *river.Job[service.FeedbackRecordsPurgeArgs],
) error {
	if _, err := w.service.Purge(ctx, job.Args.TenantID); err != nil {
		// The tenant id is not interpolated into the error: purge failures are logged and retried
		// by River, and the tenant is already carried on the job row.
		return fmt.Errorf("purge feedback records: %w", err)
	}

	return nil
}
