package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// tenantDataExecutor is the Exec-only statement surface the purge runs on
// (DELETE statements and the advisory-lock SQL). The tenant write transaction
// (tenantWriteTx) satisfies it.
type tenantDataExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// TenantDataRepository handles tenant-scoped data purge operations.
type TenantDataRepository struct {
	// db opens the purge transaction. It shares tenantWriteTxBeginner with every
	// tenant-owned write path so the purge's exclusive lock and writers' shared
	// locks coordinate on the same transaction machinery.
	db tenantWriteTxBeginner
	// purgeLockTimeout bounds how long a purge waits for in-flight tenant-owned
	// writes (shared tenant write lock holders) to drain before returning a
	// retryable conflict.
	purgeLockTimeout time.Duration
}

// NewTenantDataRepository creates a new tenant data repository.
func NewTenantDataRepository(db *pgxpool.Pool, purgeLockTimeout time.Duration) *TenantDataRepository {
	return &TenantDataRepository{db: tenantWritePool{db: db}, purgeLockTimeout: purgeLockTimeout}
}

// DeleteByTenant deletes all Hub-owned data for a tenant and returns per-resource counts.
func (r *TenantDataRepository) DeleteByTenant(ctx context.Context, tenantID string) (*models.TenantDataDeleteCounts, error) {
	return withTenantPurgeTx(ctx, r.db, tenantID, r.purgeLockTimeout, "tenant data delete",
		func(ctx context.Context, tx tenantWriteTx, tenantID string) (*models.TenantDataDeleteCounts, error) {
			// deleteTenantDataInTx needs only Exec; the adapter keeps its narrower signature (and its
			// tests) unchanged now that the shared helper hands over the full transaction.
			return deleteTenantDataInTx(ctx, tx, tenantID)
		})
}

// feedbackRecordsPurgeBatchSize bounds how many records one purge transaction deletes. Batching is
// what makes a purge resumable: a tenant's record count is unbounded, so a single transaction can
// outlive the worker's timeout, and on a rescue that transaction rolls back — deleting nothing, for
// every attempt, forever. Committing per batch means a killed purge keeps the progress it made and
// the retry continues from there. It also chunks the ingestion block: the exclusive tenant lock is
// released between batches instead of being held for the whole delete.
const feedbackRecordsPurgeBatchSize = 2000

// feedbackRecordsPurgeBatchPause is how long the purge yields between batches.
//
// Tenant-owned writes take the write lock with a non-blocking try-acquire, so they are rejected the
// moment an exclusive acquisition is queued. Without a pause the purge re-queues immediately after
// each batch commits, and a long purge rejects that tenant's ingestion more or less continuously —
// across every workspace the dataset is shared with. Yielding briefly leaves a window in which
// writers actually win the lock. It costs a few seconds per 100k records.
const feedbackRecordsPurgeBatchPause = 50 * time.Millisecond

// PurgeFeedbackRecordsByTenant deletes every feedback record for a tenant, everything derived from
// those records, and the taxonomy built on them, returning exact per-table counts. Unlike
// DeleteByTenant it leaves the tenant's *configuration* — webhooks and settings — intact, so the
// dataset stays usable and any integrator setup survives.
//
// Two phases, because the two halves have different shapes. Records are unbounded, so they go in
// committed batches (see feedbackRecordsPurgeBatchSize): resumable, and the counts reported are the
// ones actually achieved even on a partial failure. Taxonomy artifacts are bounded by the number of
// runs, so once the records are gone they are removed in one final transaction.
//
// Bounded to the records that existed when the purge started, via a high-water mark on the uuidv7
// id. Two reasons, both load-bearing: the exclusive lock is released between batches, so without a
// bound a tenant that keeps ingesting keeps feeding the loop and the purge may never reach a zero
// batch; and an operator asked to empty what they saw, not to delete feedback that arrived
// afterwards.
//
// Ordering matters: the taxonomy phase runs last so that a purge interrupted midway leaves records
// missing but the tree still standing, rather than the reverse — a tree with no records is the
// misleading state, and it is exactly what the final phase removes.
func (r *TenantDataRepository) PurgeFeedbackRecordsByTenant(
	ctx context.Context, tenantID string,
) (*models.FeedbackRecordsPurgeCounts, error) {
	total := &models.FeedbackRecordsPurgeCounts{}

	highWaterMark, hasRecords, err := r.feedbackRecordsHighWaterMark(ctx, tenantID)
	if err != nil {
		return total, err
	}

	for hasRecords {
		if err := ctx.Err(); err != nil {
			// Report the progress already committed; the retry resumes from here.
			return total, fmt.Errorf("purge feedback records for tenant: %w", err)
		}

		batch, err := r.purgeFeedbackRecordsBatch(ctx, tenantID, highWaterMark)
		if err != nil {
			return total, err
		}

		total.DeletedFeedbackRecords += batch.DeletedFeedbackRecords
		total.DeletedEmbeddings += batch.DeletedEmbeddings
		// Memberships are removed per batch alongside the records they belong to, so they are summed
		// across both phases. The taxonomy pass usually finds none left, but it can still delete some:
		// the lock is released between phases, so a record ingested and clustered in that window
		// contributes memberships that only the final pass reaches.
		total.ClusterMemberships += batch.ClusterMemberships

		if batch.DeletedFeedbackRecords == 0 {
			break
		}

		// Let the tenant's writers through before queueing the next exclusive acquisition.
		select {
		case <-ctx.Done():
			return total, fmt.Errorf("purge feedback records for tenant: %w", ctx.Err())
		case <-time.After(feedbackRecordsPurgeBatchPause):
		}
	}

	taxonomy, err := withTenantPurgeTx(ctx, r.db, tenantID, r.purgeLockTimeout, "feedback records purge taxonomy",
		func(ctx context.Context, tx tenantWriteTx, tenantID string) (*models.TenantTaxonomyDeleteCounts, error) {
			return deleteTenantTaxonomyInTx(ctx, tx, tenantID)
		})
	if err != nil {
		return total, err
	}

	total.Runs = taxonomy.Runs
	total.Clusters = taxonomy.Clusters
	total.Nodes = taxonomy.Nodes
	total.ActiveRuns = taxonomy.ActiveRuns
	total.NodeEvents = taxonomy.NodeEvents
	total.ClusterMemberships += taxonomy.ClusterMemberships

	return total, nil
}

// feedbackRecordsPurgeBatchLockAttempts bounds how many times a single batch re-tries a lock
// conflict before giving up and failing the job.
//
// A purge takes the exclusive tenant lock once per batch, so a large tenant acquires it hundreds of
// times, each acquisition bounded by the configured purge lock timeout. Without an in-loop retry a
// single slow writer anywhere in that sequence fails the whole attempt, and River's fixed retry
// budget is consumed by transient contention rather than real faults — a big, actively-written
// tenant could exhaust every attempt while making progress each time.
const feedbackRecordsPurgeBatchLockAttempts = 5

// purgeFeedbackRecordsBatch runs one batch, re-trying a tenant write conflict rather than failing
// the whole purge. Any other error is returned immediately: only lock contention is transient.
func (r *TenantDataRepository) purgeFeedbackRecordsBatch(
	ctx context.Context, tenantID string, highWaterMark uuid.UUID,
) (*models.FeedbackRecordsPurgeCounts, error) {
	var lastErr error

	for range feedbackRecordsPurgeBatchLockAttempts {
		batch, err := withTenantPurgeTx(ctx, r.db, tenantID, r.purgeLockTimeout, "feedback records purge",
			func(ctx context.Context, tx tenantWriteTx, tenantID string) (*models.FeedbackRecordsPurgeCounts, error) {
				return purgeFeedbackRecordsBatchInTx(ctx, tx, tenantID, highWaterMark, feedbackRecordsPurgeBatchSize)
			})
		if err == nil {
			return batch, nil
		}

		if !errors.Is(err, huberrors.ErrTenantWriteConflict) {
			return nil, err
		}

		lastErr = err

		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("purge feedback records batch: %w", ctxErr)
		}
	}

	return nil, lastErr
}

// feedbackRecordsHighWaterMark returns the newest record id the purge should remove, and whether the
// tenant has any records at all. Bounding on the id works because it is uuidv7, and therefore
// time-ordered.
func (r *TenantDataRepository) feedbackRecordsHighWaterMark(
	ctx context.Context, tenantID string,
) (uuid.UUID, bool, error) {
	dbTx, err := r.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("begin feedback records high-water mark transaction: %w", err)
	}

	defer func() {
		if err := dbTx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) && ctx.Err() == nil {
			slog.Error("feedback records purge: high-water mark rollback failed",
				"tenant_id_present", tenantID != "",
				"tenant_id_length", len(tenantID),
				"error", err,
			)
		}
	}()

	// ORDER BY id DESC rather than max(id): Postgres has no max() aggregate for uuid. This is the
	// largest id by definition, which is what the batches must not exceed.
	var highWaterMark uuid.UUID
	if err := dbTx.QueryRow(ctx, `
		SELECT id FROM feedback_records WHERE tenant_id = $1 ORDER BY id DESC LIMIT 1`, tenantID,
	).Scan(&highWaterMark); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No records: the loop is skipped entirely and only the taxonomy pass runs.
			return uuid.Nil, false, nil
		}

		return uuid.Nil, false, fmt.Errorf("read feedback records high-water mark: %w", err)
	}

	return highWaterMark, true, nil
}

// purgeFeedbackRecordsBatchInTx removes up to limit of a tenant's feedback records and the rows
// derived from them, returning exact per-table counts for the batch. Returns zero records deleted
// when the tenant has none left, which is what ends the loop.
//
// One statement so the three deletes share a snapshot and a single round trip. The derived tables
// would be removed by ON DELETE CASCADE from feedback_records anyway (embeddings via migration 004,
// taxonomy_cluster_memberships via the composite (feedback_record_id, tenant_id) FK in migration
// 010); they are deleted explicitly, children first, for the same reason the full tenant purge does
// it — a cascade reports no row count and callers need exact numbers. Deleting them here is
// equivalent, not additional. embeddings carries no tenant_id of its own, but every victim id comes
// from a tenant-scoped select, so the delete stays inside the tenant boundary.
//
// The rest of the taxonomy (runs, clusters, nodes, active-run pointers, node events) is removed by
// the caller's final phase once the records are gone — see PurgeFeedbackRecordsByTenant. What this
// purge never touches is webhooks and tenant_settings: those are tenant configuration, not tenant
// data. Enrichment output (sentiment, emotions, translations) needs no statement of its own — it
// lives in columns on feedback_records and goes with the row. Enrichment FAILURE markers
// (feedback_record_enrichment_failures, migration 022) do have a table of their own but are left
// to ON DELETE CASCADE: they are internal derived state, and unlike the tables above no caller has
// a use for a count of them. That makes them the one derived table here whose removal nothing
// would notice, so an integration test asserts it — see tests/feedback_records_purge_test.go.
func purgeFeedbackRecordsBatchInTx(
	ctx context.Context, transaction tenantWriteTx, tenantID string, highWaterMark uuid.UUID, limit int,
) (*models.FeedbackRecordsPurgeCounts, error) {
	counts := &models.FeedbackRecordsPurgeCounts{}

	// Deliberately unordered: the rows are being deleted, so any `limit` of them make progress, and
	// there is no (tenant_id, id) index — an ORDER BY makes every batch sort the tenant's entire
	// remaining set to keep 2000, which is quadratic over exactly the large tenants batching exists
	// for.
	err := transaction.QueryRow(ctx, `
		WITH victims AS (
			SELECT id FROM feedback_records WHERE tenant_id = $1 AND id <= $2 LIMIT $3
		),
		deleted_embeddings AS (
			DELETE FROM embeddings
			WHERE feedback_record_id IN (SELECT id FROM victims)
			RETURNING 1
		),
		deleted_memberships AS (
			DELETE FROM taxonomy_cluster_memberships
			WHERE tenant_id = $1 AND feedback_record_id IN (SELECT id FROM victims)
			RETURNING 1
		),
		deleted_records AS (
			DELETE FROM feedback_records
			WHERE tenant_id = $1 AND id IN (SELECT id FROM victims)
			RETURNING 1
		)
		SELECT
			(SELECT count(*) FROM deleted_records),
			(SELECT count(*) FROM deleted_embeddings),
			(SELECT count(*) FROM deleted_memberships)`, tenantID, highWaterMark, limit,
	).Scan(&counts.DeletedFeedbackRecords, &counts.DeletedEmbeddings, &counts.ClusterMemberships)
	if err != nil {
		return nil, fmt.Errorf("purge tenant feedback records batch: %w", err)
	}

	return counts, nil
}

// withTenantPurgeTx runs purge in a transaction holding the tenant write lock exclusively, so the
// purge is serialized against tenant-owned writes (which hold the same lock in shared mode): the
// exclusive acquisition waits for in-flight writes to drain (bounded by lockTimeout) and rejects
// new ones the moment it is queued.
//
// Shared by every tenant purge variant — the full tenant offboarding purge and the narrower
// feedback-records purge — so they cannot drift on locking, rollback or commit handling. opName
// only labels log lines.
func withTenantPurgeTx[T any](
	ctx context.Context,
	db tenantWriteTxBeginner,
	tenantID string,
	lockTimeout time.Duration,
	opName string,
	purge func(ctx context.Context, tx tenantWriteTx, tenantID string) (T, error),
) (T, error) {
	var zero T

	dbTx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return zero, fmt.Errorf("begin %s transaction: %w", opName, err)
	}

	defer func() {
		// A canceled request ctx (e.g. while waiting for the purge lock) can make
		// pgx close the connection so Rollback can't send ROLLBACK; Postgres still
		// aborts the tx and releases the advisory lock on session end. Skip logging
		// when the ctx is already done — that rollback error is expected, not a fault.
		if err := dbTx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) && ctx.Err() == nil {
			slog.Error(
				opName+": rollback failed",
				"tenant_id_present", tenantID != "",
				"tenant_id_length", len(tenantID),
				"error", err,
			)
		}
	}()

	if err := acquireTenantPurgeLock(ctx, dbTx, tenantID, lockTimeout); err != nil {
		return zero, err
	}

	result, err := purge(ctx, dbTx, tenantID)
	if err != nil {
		return zero, err
	}

	if err := dbTx.Commit(ctx); err != nil {
		slog.Error(
			opName+": commit failed",
			"tenant_id_present", tenantID != "",
			"tenant_id_length", len(tenantID),
			"error", err,
		)

		return zero, fmt.Errorf("commit %s transaction: %w", opName, err)
	}

	return result, nil
}

func deleteTenantDataInTx(
	ctx context.Context, exec tenantDataExecutor, tenantID string,
) (*models.TenantDataDeleteCounts, error) {
	embeddingTag, err := exec.Exec(ctx, `
		DELETE FROM embeddings e
		USING feedback_records fr
		WHERE e.feedback_record_id = fr.id
			AND fr.tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant embeddings: %w", err)
	}

	taxonomy, err := deleteTenantTaxonomyInTx(ctx, exec, tenantID)
	if err != nil {
		return nil, err
	}

	feedbackRecordsTag, err := exec.Exec(ctx, `
		DELETE FROM feedback_records
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant feedback records: %w", err)
	}

	// Configuration, not data — and the only thing this purge removes that the narrower
	// feedback-records purge deliberately keeps. Webhooks are integrator-configured directly against
	// the Hub (including a signing_key that reads never return), and tenant_settings holds
	// enrichment opt-outs that silently revert to "enabled" once the row is gone. Both are
	// appropriate to drop when a tenant is being deprovisioned, and only then.
	webhooksTag, err := exec.Exec(ctx, `
		DELETE FROM webhooks
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant webhooks: %w", err)
	}

	// The count is not surfaced (at most one row per tenant), mirroring the taxonomy_runs delete.
	if _, err = exec.Exec(ctx, `
		DELETE FROM tenant_settings
		WHERE tenant_id = $1`, tenantID); err != nil {
		return nil, fmt.Errorf("delete tenant settings: %w", err)
	}

	return &models.TenantDataDeleteCounts{
		DeletedFeedbackRecords:            feedbackRecordsTag.RowsAffected(),
		DeletedEmbeddings:                 embeddingTag.RowsAffected(),
		DeletedWebhooks:                   webhooksTag.RowsAffected(),
		DeletedTaxonomyRuns:               taxonomy.Runs,
		DeletedTaxonomyClusters:           taxonomy.Clusters,
		DeletedTaxonomyClusterMemberships: taxonomy.ClusterMemberships,
		DeletedTaxonomyNodes:              taxonomy.Nodes,
		DeletedTaxonomyActiveRuns:         taxonomy.ActiveRuns,
		DeletedTaxonomyNodeEvents:         taxonomy.NodeEvents,
	}, nil
}

// deleteTenantTaxonomyInTx removes every taxonomy artifact for a tenant and returns exact per-table
// counts. Shared by both purges: the offboarding purge and the narrower feedback-records purge both
// take the taxonomy, and this is the one place the ordering below is encoded.
//
// Taxonomy generation artifacts are run-scoped Hub data. Deleting feedback_records only cascades
// cluster memberships (via the membership -> feedback_records FK), leaving runs, clusters, nodes,
// active-run rows and node events orphaned. Every table is removed explicitly, children before
// parents, so each count is exact and the purge never relies on cascades. Ordering rules:
//   - node_events and cluster_memberships reference runs/nodes/clusters, so they go first.
//   - taxonomy_clusters and taxonomy_nodes have no tenant_id column; they are scoped through their
//     run via a taxonomy_runs subquery, which means taxonomy_runs MUST be deleted last (after nodes
//     and clusters) or the subquery would match nothing and orphan them.
func deleteTenantTaxonomyInTx(
	ctx context.Context, exec tenantDataExecutor, tenantID string,
) (*models.TenantTaxonomyDeleteCounts, error) {
	nodeEventsTag, err := exec.Exec(ctx, `
		DELETE FROM taxonomy_node_events
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy node events: %w", err)
	}

	clusterMembershipsTag, err := exec.Exec(ctx, `
		DELETE FROM taxonomy_cluster_memberships
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy cluster memberships: %w", err)
	}

	nodesTag, err := exec.Exec(ctx, `
		DELETE FROM taxonomy_nodes
		WHERE run_id IN (SELECT id FROM taxonomy_runs WHERE tenant_id = $1)`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy nodes: %w", err)
	}

	clustersTag, err := exec.Exec(ctx, `
		DELETE FROM taxonomy_clusters
		WHERE run_id IN (SELECT id FROM taxonomy_runs WHERE tenant_id = $1)`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy clusters: %w", err)
	}

	activeRunsTag, err := exec.Exec(ctx, `
		DELETE FROM taxonomy_active_runs
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy active runs: %w", err)
	}

	runsTag, err := exec.Exec(ctx, `
		DELETE FROM taxonomy_runs
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy runs: %w", err)
	}

	return &models.TenantTaxonomyDeleteCounts{
		Runs:               runsTag.RowsAffected(),
		Clusters:           clustersTag.RowsAffected(),
		ClusterMemberships: clusterMembershipsTag.RowsAffected(),
		Nodes:              nodesTag.RowsAffected(),
		ActiveRuns:         activeRunsTag.RowsAffected(),
		NodeEvents:         nodeEventsTag.RowsAffected(),
	}, nil
}
