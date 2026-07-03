package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

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
	dbTx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tenant data delete transaction: %w", err)
	}

	defer func() {
		// A canceled request ctx (e.g. while waiting for the purge lock) can make
		// pgx close the connection so Rollback can't send ROLLBACK; Postgres still
		// aborts the tx and releases the advisory lock on session end. Skip logging
		// when the ctx is already done — that rollback error is expected, not a fault.
		if err := dbTx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) && ctx.Err() == nil {
			slog.Error(
				"tenant data delete: rollback failed",
				"tenant_id_present", tenantID != "",
				"tenant_id_length", len(tenantID),
				"error", err,
			)
		}
	}()

	// Serialize against tenant-owned writes: writers hold the tenant write lock
	// in shared mode, so the exclusive acquisition waits for in-flight writes to
	// drain (bounded by purgeLockTimeout) and rejects new ones the moment it is
	// queued.
	if err := acquireTenantPurgeLock(ctx, dbTx, tenantID, r.purgeLockTimeout); err != nil {
		return nil, err
	}

	counts, err := deleteTenantDataInTx(ctx, dbTx, tenantID)
	if err != nil {
		return nil, err
	}

	if err := dbTx.Commit(ctx); err != nil {
		slog.Error(
			"tenant data delete: commit failed",
			"tenant_id_present", tenantID != "",
			"tenant_id_length", len(tenantID),
			"error", err,
		)

		return nil, fmt.Errorf("commit tenant data delete transaction: %w", err)
	}

	return counts, nil
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

	// Taxonomy generation artifacts are run-scoped Hub data. Deleting
	// feedback_records only cascades cluster memberships (via the membership ->
	// feedback_records FK), leaving runs, clusters, nodes, active-run rows, and
	// node events orphaned. Remove every taxonomy table explicitly here, children
	// before parents, so each delete count is exact and the purge never relies on
	// cascades. Ordering rules:
	//   - node_events and cluster_memberships reference runs/nodes/clusters, so
	//     they go first.
	//   - taxonomy_clusters and taxonomy_nodes have no tenant_id column; they are
	//     scoped through their run via a taxonomy_runs subquery, which means
	//     taxonomy_runs MUST be deleted last (after nodes and clusters) or the
	//     subquery would match nothing and orphan them.
	taxonomyNodeEventsTag, err := exec.Exec(ctx, `
		DELETE FROM taxonomy_node_events
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy node events: %w", err)
	}

	taxonomyClusterMembershipsTag, err := exec.Exec(ctx, `
		DELETE FROM taxonomy_cluster_memberships
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy cluster memberships: %w", err)
	}

	taxonomyNodesTag, err := exec.Exec(ctx, `
		DELETE FROM taxonomy_nodes
		WHERE run_id IN (SELECT id FROM taxonomy_runs WHERE tenant_id = $1)`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy nodes: %w", err)
	}

	taxonomyClustersTag, err := exec.Exec(ctx, `
		DELETE FROM taxonomy_clusters
		WHERE run_id IN (SELECT id FROM taxonomy_runs WHERE tenant_id = $1)`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy clusters: %w", err)
	}

	taxonomyActiveRunsTag, err := exec.Exec(ctx, `
		DELETE FROM taxonomy_active_runs
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy active runs: %w", err)
	}

	taxonomyRunsTag, err := exec.Exec(ctx, `
		DELETE FROM taxonomy_runs
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy runs: %w", err)
	}

	feedbackRecordsTag, err := exec.Exec(ctx, `
		DELETE FROM feedback_records
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant feedback records: %w", err)
	}

	webhooksTag, err := exec.Exec(ctx, `
		DELETE FROM webhooks
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant webhooks: %w", err)
	}

	// tenant_settings is tenant-owned, so a purge must remove it too. The count is
	// not surfaced (at most one row per tenant), mirroring the taxonomy_runs delete.
	if _, err = exec.Exec(ctx, `
		DELETE FROM tenant_settings
		WHERE tenant_id = $1`, tenantID); err != nil {
		return nil, fmt.Errorf("delete tenant settings: %w", err)
	}

	return &models.TenantDataDeleteCounts{
		DeletedFeedbackRecords:            feedbackRecordsTag.RowsAffected(),
		DeletedEmbeddings:                 embeddingTag.RowsAffected(),
		DeletedWebhooks:                   webhooksTag.RowsAffected(),
		DeletedTaxonomyRuns:               taxonomyRunsTag.RowsAffected(),
		DeletedTaxonomyClusters:           taxonomyClustersTag.RowsAffected(),
		DeletedTaxonomyClusterMemberships: taxonomyClusterMembershipsTag.RowsAffected(),
		DeletedTaxonomyNodes:              taxonomyNodesTag.RowsAffected(),
		DeletedTaxonomyActiveRuns:         taxonomyActiveRunsTag.RowsAffected(),
		DeletedTaxonomyNodeEvents:         taxonomyNodeEventsTag.RowsAffected(),
	}, nil
}
