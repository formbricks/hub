package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/formbricks/hub/internal/huberrors"
)

func newRecordsPurgeRepo(transaction *fakeTenantWriteTx) *TenantDataRepository {
	return &TenantDataRepository{db: &fakeTenantWriteDB{tx: transaction}, purgeLockTimeout: 5 * time.Second}
}

// The batch statement is a single QueryRow, so the fake transaction's row results drive the loop:
// each entry is one batch's (records, embeddings, memberships) counts, and a zero-record batch ends
// the purge.
func purgeBatches(batches ...[]int64) *fakeTenantWriteTx {
	return &fakeTenantWriteTx{
		fakeTenantDataExecutor: fakeTenantDataExecutor{tags: purgeLockTags()},
		purgeBatchRows:         batches,
		rollbackErr:            pgx.ErrTxClosed,
	}
}

func TestTenantDataRepository_PurgeFeedbackRecordsByTenant(t *testing.T) {
	t.Run("sums the batches and stops on an empty one", func(t *testing.T) {
		transaction := purgeBatches([]int64{2000, 1500, 300}, []int64{40, 12, 8}, []int64{0, 0, 0})

		counts, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123")
		if err != nil {
			t.Fatalf("PurgeFeedbackRecordsByTenant() error = %v", err)
		}

		if counts.DeletedFeedbackRecords != 2040 {
			t.Fatalf("DeletedFeedbackRecords = %d, want 2040", counts.DeletedFeedbackRecords)
		}

		if counts.DeletedEmbeddings != 1512 {
			t.Fatalf("DeletedEmbeddings = %d, want 1512", counts.DeletedEmbeddings)
		}

		if counts.DeletedTaxonomyClusterMemberships != 308 {
			t.Fatalf("DeletedTaxonomyClusterMemberships = %d, want 308", counts.DeletedTaxonomyClusterMemberships)
		}
	})

	// The whole point of batching: each batch is its own transaction, so a purge that dies partway
	// keeps what it deleted and the retry resumes. A single transaction would roll everything back
	// on every rescue, and a tenant too large for one attempt could never be purged at all.
	t.Run("commits every batch so progress survives a failure", func(t *testing.T) {
		transaction := purgeBatches([]int64{2000, 0, 0}, []int64{2000, 0, 0})
		transaction.purgeBatchErrAt = 3 // the third batch fails

		counts, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123")
		if err == nil {
			t.Fatal("PurgeFeedbackRecordsByTenant() error = nil, want the batch failure")
		}

		if counts == nil || counts.DeletedFeedbackRecords != 4000 {
			t.Fatalf("counts = %+v, want the 4000 records already committed", counts)
		}

		if transaction.commits != 2 {
			t.Fatalf("commits = %d, want 2 (one per successful batch)", transaction.commits)
		}
	})

	t.Run("locks the tenant exclusively for every batch", func(t *testing.T) {
		transaction := purgeBatches([]int64{10, 0, 0}, []int64{0, 0, 0})

		if _, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123"); err != nil {
			t.Fatalf("PurgeFeedbackRecordsByTenant() error = %v", err)
		}

		lockAcquisitions := 0

		for i, query := range transaction.queries {
			if strings.Contains(query, "pg_advisory_xact_lock(hashtextextended($1, 0))") {
				lockAcquisitions++

				args := transaction.args[i]
				if len(args) != 1 || args[0] != TenantWriteLockKey("org-123") {
					t.Fatalf("lock args = %#v, want the tenant write lock key", args)
				}
			}
		}

		if lockAcquisitions != 2 {
			t.Fatalf("exclusive lock taken %d times, want once per batch (2)", lockAcquisitions)
		}
	})

	// The purge must stay narrower than the offboarding purge. If it ever starts deleting the
	// taxonomy structure, webhooks or settings, a purged dataset silently loses its topic tree.
	t.Run("touches only records and rows derived from them", func(t *testing.T) {
		transaction := purgeBatches([]int64{10, 5, 3}, []int64{0, 0, 0})

		if _, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123"); err != nil {
			t.Fatalf("PurgeFeedbackRecordsByTenant() error = %v", err)
		}

		statements := strings.Join(transaction.purgeBatchQueries, "\n")
		for _, table := range []string{
			"taxonomy_runs",
			"taxonomy_clusters",
			"taxonomy_nodes",
			"taxonomy_active_runs",
			"taxonomy_node_events",
			"webhooks",
			"tenant_settings",
		} {
			if strings.Contains(statements, "DELETE FROM "+table+"\n") ||
				strings.Contains(statements, "DELETE FROM "+table+" ") {
				t.Fatalf("purge deleted from %q; it must only remove records and rows derived from them", table)
			}
		}

		for _, want := range []string{"DELETE FROM embeddings", "DELETE FROM taxonomy_cluster_memberships", "DELETE FROM feedback_records"} {
			if !strings.Contains(statements, want) {
				t.Fatalf("purge statement missing %q", want)
			}
		}
	})

	// Every delete is scoped by the tenant, and the batch is bounded — an unscoped or unbounded
	// statement here would empty the table or hold the lock indefinitely.
	t.Run("scopes and bounds every batch", func(t *testing.T) {
		transaction := purgeBatches([]int64{0, 0, 0})

		if _, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123"); err != nil {
			t.Fatalf("PurgeFeedbackRecordsByTenant() error = %v", err)
		}

		statement := transaction.purgeBatchQueries[0]
		if !strings.Contains(statement, "tenant_id = $1") {
			t.Fatalf("batch is not tenant-scoped: %s", statement)
		}

		if !strings.Contains(statement, "LIMIT $2") {
			t.Fatalf("batch is not bounded: %s", statement)
		}

		args := transaction.purgeBatchArgs[0]
		if len(args) != 2 || args[0] != "org-123" || args[1] != feedbackRecordsPurgeBatchSize {
			t.Fatalf("batch args = %#v, want tenant + batch size", args)
		}
	})

	t.Run("lock timeout returns a retryable conflict without deleting", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				tags:       purgeLockTags(),
				errAtQuery: 2,
				err:        &pgconn.PgError{Code: lockNotAvailableSQLState},
			},
			rollbackErr: pgx.ErrTxClosed,
		}

		counts, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123")
		if !errors.Is(err, huberrors.ErrTenantWriteConflict) {
			t.Fatalf("error = %v, want tenant write conflict", err)
		}

		if counts == nil || counts.DeletedFeedbackRecords != 0 {
			t.Fatalf("counts = %+v, want zero deletions", counts)
		}

		if transaction.commits != 0 {
			t.Fatal("transaction was committed despite the lock conflict")
		}

		if len(transaction.purgeBatchQueries) != 0 {
			t.Fatal("a batch ran despite the lock conflict")
		}
	})

	// A cancelled context must stop the loop rather than spinning; the committed batches stand.
	t.Run("stops on a cancelled context and keeps committed progress", func(t *testing.T) {
		transaction := purgeBatches([]int64{2000, 0, 0}, []int64{2000, 0, 0}, []int64{0, 0, 0})
		ctx, cancel := context.WithCancel(context.Background())
		transaction.afterBatch = func(batches int) {
			if batches == 1 {
				cancel()
			}
		}

		counts, err := newRecordsPurgeRepo(transaction).PurgeFeedbackRecordsByTenant(ctx, "org-123")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}

		if counts.DeletedFeedbackRecords != 2000 {
			t.Fatalf("DeletedFeedbackRecords = %d, want the 2000 already committed", counts.DeletedFeedbackRecords)
		}
	})
}
