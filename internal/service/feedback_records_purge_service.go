package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/models"
)

// ErrPurgeInserterNotConfigured is returned when a purge is requested from a process built without
// a job inserter (hub-worker performs purges but never enqueues them).
var ErrPurgeInserterNotConfigured = errors.New("feedback records purge inserter not configured")

// FeedbackRecordsPurgeRepository is the repository surface the purge needs.
type FeedbackRecordsPurgeRepository interface {
	PurgeFeedbackRecordsByTenant(ctx context.Context, tenantID string) (*models.FeedbackRecordsPurgeCounts, error)
}

// FeedbackRecordsPurgeService owns the two halves of a tenant feedback-records purge: accepting the
// request (enqueue) and performing it (the worker's call).
//
// They are split because the purge is unbounded and must not run on the request path. The API
// process only ever reaches Enqueue — its River client is insert-only — while hub-worker reaches
// Purge. Keeping both here means the tenant normalization rule is written once and applies to
// whichever entry point is used.
// The queue and retry budget are not constructor parameters: unlike the enrichment jobs they have
// no config knob, and both are properties of the job kind itself (see FeedbackRecordsPurgeArgs).
type FeedbackRecordsPurgeService struct {
	repo     FeedbackRecordsPurgeRepository
	inserter RiverJobInserter
}

// NewFeedbackRecordsPurgeService creates the purge service. inserter may be nil in a process that
// only performs purges and never enqueues them (hub-worker); Enqueue then reports a clear error
// rather than panicking.
func NewFeedbackRecordsPurgeService(
	repo FeedbackRecordsPurgeRepository, inserter RiverJobInserter,
) *FeedbackRecordsPurgeService {
	return &FeedbackRecordsPurgeService{repo: repo, inserter: inserter}
}

// Enqueue accepts a purge request for a tenant and schedules the job that performs it.
//
// Idempotent by design: the job is unique by tenant across River's in-flight states, so requesting
// a purge while one is already running collapses into that run and still reports accepted. A purge
// requested after the previous one finished is a new run (see FeedbackRecordsPurgeArgs).
func (s *FeedbackRecordsPurgeService) Enqueue(
	ctx context.Context, tenantID string,
) (*models.FeedbackRecordsPurgeAcceptedResponse, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(tenantID)
	if err != nil {
		return nil, err
	}

	if s.inserter == nil {
		return nil, ErrPurgeInserterNotConfigured
	}

	if _, err := s.inserter.Insert(ctx, FeedbackRecordsPurgeArgs{TenantID: normalizedTenantID}, &river.InsertOpts{
		Queue:       FeedbackRecordsPurgeQueueName,
		MaxAttempts: FeedbackRecordsPurgeMaxAttempts,
		// Unique by TenantID across the IN-FLIGHT states only. ByState must be spelled out:
		// River's default set includes `completed`, and with no ByPeriod the uniqueness window is
		// unbounded — so the default would make the first purge of a tenant the only one that ever
		// runs, silently skipping every later request as a duplicate for as long as the completed
		// row is retained (forever, if a deployment disables completed-job cleanup).
		//
		// Available/Pending/Running/Scheduled are exactly the states River requires ByState to
		// include, so this is the narrowest legal set: concurrent requests still collapse into the
		// running purge, and a purge requested after the previous one finished starts a new run.
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: feedbackRecordsPurgeUniqueStates()},
	}); err != nil {
		return nil, fmt.Errorf("enqueue feedback records purge: %w", err)
	}

	return &models.FeedbackRecordsPurgeAcceptedResponse{
		TenantID: normalizedTenantID,
		Status:   models.FeedbackRecordsPurgeStatusAccepted,
		Message:  "Feedback records purge accepted for " + normalizedTenantID,
	}, nil
}

// Purge performs the purge for a tenant. Called by the worker, never from the request path.
//
// The tenant is re-normalized here rather than trusted from the job args: enqueue-time validation
// is not a substitute for scoping the work at execution time, and a job's args outlive the request
// that created them.
func (s *FeedbackRecordsPurgeService) Purge(
	ctx context.Context, tenantID string,
) (*models.FeedbackRecordsPurgeCounts, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(tenantID)
	if err != nil {
		return nil, err
	}

	counts, err := s.repo.PurgeFeedbackRecordsByTenant(ctx, normalizedTenantID)
	if err != nil {
		return nil, fmt.Errorf("purge feedback records for tenant: %w", err)
	}

	slog.Info("feedback records purge: complete",
		"tenant_id_length", len(normalizedTenantID),
		"deleted_feedback_records", counts.DeletedFeedbackRecords,
		"deleted_embeddings", counts.DeletedEmbeddings,
		"deleted_taxonomy_cluster_memberships", counts.DeletedTaxonomyClusterMemberships,
	)

	return counts, nil
}
