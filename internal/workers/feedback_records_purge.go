package workers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

// feedbackRecordsPurgeService is the minimal interface the worker needs.
type feedbackRecordsPurgeService interface {
	Purge(ctx context.Context, tenantID string) (*models.FeedbackRecordsPurgeCounts, error)
}

// FeedbackRecordsPurgeWorker deletes every feedback record for one tenant, everything derived from
// those records, and the taxonomy built on them, keeping the tenant's configuration. It runs off the
// request path because the delete is unbounded and would otherwise outlive the API server's write
// timeout on a large tenant.
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
//
// A failure is logged here rather than left to River. River reports retries and final discards at
// INFO, and hub-worker does not set river.Config.Logger, so its fallback logger sits at WARN and
// filters both — a failing purge would otherwise leave no trace outside the river_job.errors column
// while the dataset sits partly emptied. Matches webhook_dispatch and feedback_embedding in
// separating a retryable attempt from the last one.
func (w *FeedbackRecordsPurgeWorker) Work(
	ctx context.Context, job *river.Job[service.FeedbackRecordsPurgeArgs],
) error {
	counts, err := w.service.Purge(ctx, job.Args.TenantID)
	if err != nil {
		logPurgeFailure(job, counts, err)

		// The tenant id is not interpolated into the error: it is already carried on the job row
		// and in the log line above.
		return fmt.Errorf("purge feedback records: %w", err)
	}

	return nil
}

// logPurgeFailure reports a failed attempt, including how much the attempt committed before it
// failed. The partial counts matter: batches commit as they go, so a failed purge has usually
// already removed records, and the retry resumes from there rather than restarting.
func logPurgeFailure(
	job *river.Job[service.FeedbackRecordsPurgeArgs], counts *models.FeedbackRecordsPurgeCounts, err error,
) {
	attrs := []any{
		"tenant_id", job.Args.TenantID,
		"job_id", job.ID,
		"attempt", job.Attempt,
		"max_attempts", job.MaxAttempts,
		"error", err,
	}

	// counts is nil only when the purge failed before the repository ran (tenant normalization).
	if counts != nil {
		attrs = append(attrs,
			"deleted_feedback_records", counts.DeletedFeedbackRecords,
			"deleted_embeddings", counts.DeletedEmbeddings,
			"deleted_taxonomy_cluster_memberships", counts.ClusterMemberships,
			"deleted_taxonomy_run_input_records", counts.InputRecords,
			"deleted_taxonomy_runs", counts.Runs,
			"deleted_taxonomy_nodes", counts.Nodes,
		)
	}

	if job.Attempt >= job.MaxAttempts {
		// Terminal: River will not try again, so the dataset stays in whatever partial state the
		// counts above describe until someone requests another purge.
		slog.Error("feedback records purge: failed permanently", attrs...)

		return
	}

	slog.Warn("feedback records purge: attempt failed, will retry", attrs...)
}
