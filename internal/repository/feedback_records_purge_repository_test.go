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

// recordsPurgeDeleteTags returns command tags for the three DELETE statements
// purgeFeedbackRecordsInTx issues, in execution order, each with a distinct row count so the
// per-table count mapping is pinned rather than coincidentally correct.
func recordsPurgeDeleteTags() []pgconn.CommandTag {
	return []pgconn.CommandTag{
		pgconn.NewCommandTag("DELETE 2"),  // embeddings
		pgconn.NewCommandTag("DELETE 12"), // taxonomy_cluster_memberships
		pgconn.NewCommandTag("DELETE 30"), // feedback_records
	}
}

func newRecordsPurgeRepo(transaction *fakeTenantWriteTx) *TenantDataRepository {
	return &TenantDataRepository{db: &fakeTenantWriteDB{tx: transaction}, purgeLockTimeout: 5 * time.Second}
}

func TestTenantDataRepository_PurgeFeedbackRecordsByTenant(t *testing.T) {
	t.Run("locks tenant exclusively, commits, and returns per-table counts", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				tags: append(purgeLockTags(), recordsPurgeDeleteTags()...),
			},
			rollbackErr: pgx.ErrTxClosed,
		}

		counts, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123")
		if err != nil {
			t.Fatalf("PurgeFeedbackRecordsByTenant() error = %v", err)
		}

		if counts.DeletedEmbeddings != 2 {
			t.Fatalf("DeletedEmbeddings = %d, want 2", counts.DeletedEmbeddings)
		}

		if counts.DeletedTaxonomyClusterMemberships != 12 {
			t.Fatalf("DeletedTaxonomyClusterMemberships = %d, want 12", counts.DeletedTaxonomyClusterMemberships)
		}

		if counts.DeletedFeedbackRecords != 30 {
			t.Fatalf("DeletedFeedbackRecords = %d, want 30", counts.DeletedFeedbackRecords)
		}

		if !transaction.committed {
			t.Fatal("transaction was not committed")
		}

		if len(transaction.queries) != 6 {
			t.Fatalf("queries = %d, want 6 (3 lock statements + 3 deletes)", len(transaction.queries))
		}

		// The purge holds the tenant write lock exclusively, same as the full tenant purge: an
		// unbounded tenant-wide delete must not race concurrent ingestion.
		assertQueryContains(t, transaction.queries[0], "set_config('lock_timeout', $1, true)")
		assertQueryContains(t, transaction.queries[1], "pg_advisory_xact_lock(hashtextextended($1, 0))")
		assertQueryContains(t, transaction.queries[2], "set_config('lock_timeout', '0', true)")

		if len(transaction.args[1]) != 1 || transaction.args[1][0] != TenantWriteLockKey("org-123") {
			t.Fatalf("lock args = %#v, want tenant write lock key", transaction.args[1])
		}
	})

	// Children before parents, so each count is exact rather than swallowed by a cascade.
	t.Run("deletes derived rows before the records they hang off", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				tags: append(purgeLockTags(), recordsPurgeDeleteTags()...),
			},
			rollbackErr: pgx.ErrTxClosed,
		}

		if _, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123"); err != nil {
			t.Fatalf("PurgeFeedbackRecordsByTenant() error = %v", err)
		}

		assertQueryContains(t, transaction.queries[3], "DELETE FROM embeddings")
		assertQueryContains(t, transaction.queries[4], "DELETE FROM taxonomy_cluster_memberships")
		assertQueryContains(t, transaction.queries[5], "DELETE FROM feedback_records")
	})

	// The whole point of this purge is that it is narrower than DeleteByTenant. If it ever starts
	// touching the tenant's taxonomy structure, webhooks or settings, the dataset stops being usable
	// after a purge and the endpoint has silently become the offboarding purge.
	t.Run("leaves taxonomy structure, webhooks and settings untouched", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				tags: append(purgeLockTags(), recordsPurgeDeleteTags()...),
			},
			rollbackErr: pgx.ErrTxClosed,
		}

		if _, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123"); err != nil {
			t.Fatalf("PurgeFeedbackRecordsByTenant() error = %v", err)
		}

		for _, table := range []string{
			"taxonomy_runs",
			"taxonomy_clusters",
			"taxonomy_nodes",
			"taxonomy_active_runs",
			"taxonomy_node_events",
			"webhooks",
			"tenant_settings",
		} {
			for _, query := range transaction.queries {
				if strings.Contains(query, "DELETE FROM "+table) {
					t.Fatalf("purge deleted from %q; it must only remove records and rows derived from them", table)
				}
			}
		}
	})

	// Every statement is scoped by the tenant. An unscoped delete here would empty the table.
	t.Run("scopes every delete to the tenant", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				tags: append(purgeLockTags(), recordsPurgeDeleteTags()...),
			},
			rollbackErr: pgx.ErrTxClosed,
		}

		if _, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123"); err != nil {
			t.Fatalf("PurgeFeedbackRecordsByTenant() error = %v", err)
		}

		for i, query := range transaction.queries[3:] {
			if !strings.Contains(query, "tenant_id = $1") {
				t.Fatalf("delete %d is not tenant-scoped: %s", i, query)
			}

			args := transaction.args[i+3]
			if len(args) != 1 || args[0] != "org-123" {
				t.Fatalf("delete %d args = %#v, want the tenant id", i, args)
			}
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

		if counts != nil {
			t.Fatalf("counts = %+v, want nil", counts)
		}

		if transaction.committed {
			t.Fatal("transaction was committed despite the lock conflict")
		}

		if len(transaction.queries) != 2 {
			t.Fatalf("queries = %d, want 2 (lock attempt only, no deletes)", len(transaction.queries))
		}
	})

	t.Run("a failed delete rolls back rather than committing a partial purge", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				tags:       append(purgeLockTags(), recordsPurgeDeleteTags()...),
				errAtQuery: 5, // the taxonomy_cluster_memberships delete
			},
			rollbackErr: pgx.ErrTxClosed,
		}

		if _, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123"); err == nil {
			t.Fatal("PurgeFeedbackRecordsByTenant() error = nil, want the delete failure")
		}

		if transaction.committed {
			t.Fatal("transaction was committed despite a failed delete")
		}

		if !transaction.rolledBack {
			t.Fatal("deferred rollback was not called")
		}
	})
}
