package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// foreignKeyViolationSQLState is the SQLSTATE Postgres reports when a referenced row is missing.
// Here it means the feedback record was deleted between the enrichment attempt and the marker
// write, which is a benign race rather than an error worth failing a job over.
const foreignKeyViolationSQLState = "23503"

// EnrichmentFailuresRepository writes the durable failure markers the status endpoint counts and
// the reconciler reads. It is deliberately separate from FeedbackRecordsRepository: this is
// bookkeeping about a record rather than the record itself, and keeping it apart stops the
// primary records repository growing another concern.
type EnrichmentFailuresRepository struct {
	db *pgxpool.Pool
}

// NewEnrichmentFailuresRepository creates an enrichment failures repository.
func NewEnrichmentFailuresRepository(db *pgxpool.Pool) *EnrichmentFailuresRepository {
	return &EnrichmentFailuresRepository{db: db}
}

// recordEnrichmentFailureSQL upserts the marker for one (record, enrichment).
//
// Upsert rather than insert because a record can fail again after a later edit re-enqueues it, and
// the newest outcome is the one worth keeping — including a transition from terminal back to
// transient, or the reverse, when the text changed.
//
// The tenant write-lock gate follows the convention for inserts whose tenant is known up front
// (see tenant_write_lock.go): zero rows means a purge holds the tenant exclusively. tenant_id is
// deliberately NOT updated on conflict — a record cannot change tenant, so an incoming value that
// disagreed with the stored one would be a bug worth preserving the evidence of rather than
// silently overwriting.
// enrichmentFailureLockKeyParam is the placeholder carrying the tenant write-lock key, after the
// six inserted values.
const enrichmentFailureLockKeyParam = 7

// A var rather than a const because tenantWriteLockGate is a function call; built once at package
// init, and every fragment is a compile-time literal, never caller input.
var recordEnrichmentFailureSQL = `
	INSERT INTO feedback_record_enrichment_failures
		(feedback_record_id, enrichment, tenant_id, failed_at, attempts, terminal, reason)
	SELECT $1, $2, $3, NOW(), $4, $5, $6
	WHERE ` + tenantWriteLockGate(enrichmentFailureLockKeyParam) + `
	ON CONFLICT (feedback_record_id, enrichment) DO UPDATE SET
		failed_at = NOW(),
		attempts  = EXCLUDED.attempts,
		terminal  = EXCLUDED.terminal,
		reason    = EXCLUDED.reason`

// RecordFailure persists one enrichment failure.
//
// Two outcomes are reported as ErrTenantWriteConflict rather than as errors of their own, because
// both mean "the record is going away and the marker is moot": the tenant write lock being refused
// (a purge is running), and the foreign key failing (the record was deleted between the enrichment
// attempt and this write). Callers treat that as a benign skip — a marker is bookkeeping, and
// failing an enrichment job over it would be the wrong trade.
func (r *EnrichmentFailuresRepository) RecordFailure(ctx context.Context, failure models.EnrichmentFailure) error {
	tag, err := r.db.Exec(ctx, recordEnrichmentFailureSQL,
		failure.FeedbackRecordID,
		failure.Enrichment,
		failure.TenantID,
		failure.Attempts,
		failure.Terminal,
		failure.Reason,
		TenantWriteLockKey(failure.TenantID),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolationSQLState {
			return huberrors.NewTenantWriteConflictError("feedback record no longer exists; failure marker skipped")
		}

		return fmt.Errorf("record enrichment failure: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return huberrors.NewTenantWriteConflictError("tenant data purge in progress for this tenant; failure marker skipped")
	}

	return nil
}
