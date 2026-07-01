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
	"github.com/formbricks/hub/internal/models"
)

// fakeTenantDataExecutor is the shared Exec recorder embedded by
// fakeTenantWriteTx (see tenant_write_lock_test.go); the purge tests below
// drive that same fake transaction, which satisfies tenantWriteTxBeginner.
type fakeTenantDataExecutor struct {
	tags       []pgconn.CommandTag
	errAtQuery int
	err        error
	queries    []string
	args       [][]any
}

func (f *fakeTenantDataExecutor) Exec(
	_ context.Context, sql string, arguments ...any,
) (pgconn.CommandTag, error) {
	f.queries = append(f.queries, sql)
	f.args = append(f.args, arguments)

	if f.errAtQuery == len(f.queries) {
		if f.err != nil {
			return pgconn.CommandTag{}, f.err
		}

		return pgconn.CommandTag{}, errors.New("exec failed")
	}

	tagIndex := len(f.queries) - 1
	if tagIndex >= len(f.tags) {
		return pgconn.CommandTag{}, nil
	}

	return f.tags[tagIndex], nil
}

// purgeLockTags are the command tags for the three lock-related statements
// (set_config, advisory lock, set_config reset) that precede the deletes.
func purgeLockTags() []pgconn.CommandTag {
	return []pgconn.CommandTag{
		pgconn.NewCommandTag("SELECT 1"),
		pgconn.NewCommandTag("SELECT 1"),
		pgconn.NewCommandTag("SELECT 1"),
	}
}

// tenantDeleteTags returns command tags for the ten DELETE statements
// deleteTenantDataInTx issues, in execution order, each with a distinct row
// count so tests can assert the per-table count mapping (see
// assertTenantDeleteCounts).
func tenantDeleteTags() []pgconn.CommandTag {
	return []pgconn.CommandTag{
		pgconn.NewCommandTag("DELETE 2"),  // embeddings
		pgconn.NewCommandTag("DELETE 11"), // taxonomy_node_events
		pgconn.NewCommandTag("DELETE 12"), // taxonomy_cluster_memberships
		pgconn.NewCommandTag("DELETE 13"), // taxonomy_nodes
		pgconn.NewCommandTag("DELETE 14"), // taxonomy_clusters
		pgconn.NewCommandTag("DELETE 15"), // taxonomy_active_runs
		pgconn.NewCommandTag("DELETE 16"), // taxonomy_runs
		pgconn.NewCommandTag("DELETE 3"),  // feedback_records
		pgconn.NewCommandTag("DELETE 1"),  // webhooks
		pgconn.NewCommandTag("DELETE 99"), // tenant_settings (count not surfaced)
	}
}

// assertTenantDeleteCounts verifies the counts produced from tenantDeleteTags(),
// including every taxonomy table (tenant_settings is intentionally not surfaced).
func assertTenantDeleteCounts(t *testing.T, counts *models.TenantDataDeleteCounts) {
	t.Helper()

	want := models.TenantDataDeleteCounts{
		DeletedFeedbackRecords:            3,
		DeletedEmbeddings:                 2,
		DeletedWebhooks:                   1,
		DeletedTaxonomyRuns:               16,
		DeletedTaxonomyClusters:           14,
		DeletedTaxonomyClusterMemberships: 12,
		DeletedTaxonomyNodes:              13,
		DeletedTaxonomyActiveRuns:         15,
		DeletedTaxonomyNodeEvents:         11,
	}

	if counts == nil {
		t.Fatalf("counts = nil, want %+v", want)
	}

	if *counts != want {
		t.Fatalf("counts = %+v, want %+v", *counts, want)
	}
}

func TestTenantDataRepository_DeleteByTenant(t *testing.T) {
	t.Run("locks tenant exclusively, commits transaction, and returns counts", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				tags: append(purgeLockTags(), tenantDeleteTags()...),
			},
			rollbackErr: pgx.ErrTxClosed,
		}
		repo := &TenantDataRepository{db: &fakeTenantWriteDB{tx: transaction}, purgeLockTimeout: 5 * time.Second}

		counts, err := repo.DeleteByTenant(context.Background(), "org-123")
		if err != nil {
			t.Fatalf("DeleteByTenant() error = %v", err)
		}

		assertTenantDeleteCounts(t, counts)

		if !transaction.committed {
			t.Fatal("transaction was not committed")
		}

		if !transaction.rolledBack {
			t.Fatal("deferred rollback was not called")
		}

		if len(transaction.queries) != 13 {
			t.Fatalf("queries = %d, want 13 (3 lock statements + 10 deletes)", len(transaction.queries))
		}

		assertQueryContains(t, transaction.queries[0], "set_config('lock_timeout', $1, true)")
		assertQueryContains(t, transaction.queries[1], "pg_advisory_xact_lock(hashtextextended($1, 0))")
		assertQueryContains(t, transaction.queries[2], "set_config('lock_timeout', '0', true)")
		assertQueryContains(t, transaction.queries[3], "DELETE FROM embeddings")
		assertQueryContains(t, transaction.queries[12], "DELETE FROM tenant_settings")

		if len(transaction.args[1]) != 1 || transaction.args[1][0] != TenantWriteLockKey("org-123") {
			t.Fatalf("lock args = %#v, want tenant write lock key", transaction.args[1])
		}
	})

	t.Run("lock timeout returns tenant write conflict without deletes", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				errAtQuery: 2,
				err:        &pgconn.PgError{Code: lockNotAvailableSQLState},
			},
		}
		repo := &TenantDataRepository{db: &fakeTenantWriteDB{tx: transaction}, purgeLockTimeout: time.Second}

		counts, err := repo.DeleteByTenant(context.Background(), "org-123")
		if !errors.Is(err, huberrors.ErrTenantWriteConflict) {
			t.Fatalf("DeleteByTenant() error = %v, want tenant write conflict", err)
		}

		if counts != nil {
			t.Fatalf("counts = %+v, want nil", counts)
		}

		if transaction.committed {
			t.Fatal("transaction was committed after lock timeout")
		}

		if !transaction.rolledBack {
			t.Fatal("transaction was not rolled back")
		}

		for _, query := range transaction.queries {
			if strings.Contains(query, "DELETE FROM") {
				t.Fatalf("delete executed despite lock timeout: %q", query)
			}
		}
	})

	t.Run("rolls back and returns delete error", func(t *testing.T) {
		rollbackErr := errors.New("rollback failed")
		transaction := &fakeTenantWriteTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				tags:       purgeLockTags(),
				errAtQuery: 5,
			},
			rollbackErr: rollbackErr,
		}
		repo := &TenantDataRepository{db: &fakeTenantWriteDB{tx: transaction}}

		counts, err := repo.DeleteByTenant(context.Background(), "org-123")
		if err == nil {
			t.Fatal("DeleteByTenant() error = nil, want error")
		}

		if counts != nil {
			t.Fatalf("counts = %+v, want nil", counts)
		}

		if transaction.committed {
			t.Fatal("transaction was committed after delete error")
		}

		if !transaction.rolledBack {
			t.Fatal("transaction was not rolled back")
		}
	})

	t.Run("returns begin transaction error", func(t *testing.T) {
		beginErr := errors.New("begin failed")
		repo := &TenantDataRepository{db: &fakeTenantWriteDB{beginErr: beginErr}}

		counts, err := repo.DeleteByTenant(context.Background(), "org-123")
		if !errors.Is(err, beginErr) {
			t.Fatalf("DeleteByTenant() error = %v, want begin error", err)
		}

		if counts != nil {
			t.Fatalf("counts = %+v, want nil", counts)
		}
	})

	t.Run("returns commit error", func(t *testing.T) {
		commitErr := errors.New("commit failed")
		transaction := &fakeTenantWriteTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				tags: append(purgeLockTags(), tenantDeleteTags()...),
			},
			commitErr:   commitErr,
			rollbackErr: pgx.ErrTxClosed,
		}
		repo := &TenantDataRepository{db: &fakeTenantWriteDB{tx: transaction}}

		counts, err := repo.DeleteByTenant(context.Background(), "org-123")
		if !errors.Is(err, commitErr) {
			t.Fatalf("DeleteByTenant() error = %v, want commit error", err)
		}

		if counts != nil {
			t.Fatalf("counts = %+v, want nil", counts)
		}
	})
}

func TestDeleteTenantDataInTx(t *testing.T) {
	t.Run("deletes explicit tenant owned tables and returns counts", func(t *testing.T) {
		exec := &fakeTenantDataExecutor{tags: tenantDeleteTags()}

		counts, err := deleteTenantDataInTx(context.Background(), exec, "org-123")
		if err != nil {
			t.Fatalf("deleteTenantDataInTx() error = %v", err)
		}

		assertTenantDeleteCounts(t, counts)

		if len(exec.queries) != 10 {
			t.Fatalf("queries = %d, want 10", len(exec.queries))
		}

		// Children before parents, with taxonomy_runs deleted after the
		// run-scoped nodes/clusters and before feedback_records.
		assertQueryContains(t, exec.queries[0], "DELETE FROM embeddings")
		assertQueryContains(t, exec.queries[0], "USING feedback_records")
		assertQueryContains(t, exec.queries[1], "DELETE FROM taxonomy_node_events")
		assertQueryContains(t, exec.queries[2], "DELETE FROM taxonomy_cluster_memberships")
		assertQueryContains(t, exec.queries[3], "DELETE FROM taxonomy_nodes")
		assertQueryContains(t, exec.queries[4], "DELETE FROM taxonomy_clusters")
		assertQueryContains(t, exec.queries[5], "DELETE FROM taxonomy_active_runs")
		assertQueryContains(t, exec.queries[6], "DELETE FROM taxonomy_runs")
		assertQueryContains(t, exec.queries[7], "DELETE FROM feedback_records")
		assertQueryContains(t, exec.queries[8], "DELETE FROM webhooks")
		assertQueryContains(t, exec.queries[9], "DELETE FROM tenant_settings")

		// taxonomy_nodes and taxonomy_clusters have no tenant_id column, so they
		// must be scoped through their run via a taxonomy_runs subquery.
		assertQueryContains(t, exec.queries[3], "SELECT id FROM taxonomy_runs WHERE tenant_id = $1")
		assertQueryContains(t, exec.queries[4], "SELECT id FROM taxonomy_runs WHERE tenant_id = $1")

		for queryIndex, args := range exec.args {
			if len(args) != 1 || args[0] != "org-123" {
				t.Fatalf("query %d args = %#v, want tenant id", queryIndex, args)
			}
		}
	})

	t.Run("stops after embeddings delete error", func(t *testing.T) {
		exec := &fakeTenantDataExecutor{errAtQuery: 1}

		counts, err := deleteTenantDataInTx(context.Background(), exec, "org-123")
		if err == nil {
			t.Fatal("deleteTenantDataInTx() error = nil, want error")
		}

		if counts != nil {
			t.Fatalf("counts = %+v, want nil", counts)
		}

		if len(exec.queries) != 1 {
			t.Fatalf("queries = %d, want 1", len(exec.queries))
		}
	})

	t.Run("stops after taxonomy node events delete error", func(t *testing.T) {
		exec := &fakeTenantDataExecutor{tags: tenantDeleteTags(), errAtQuery: 2}

		counts, err := deleteTenantDataInTx(context.Background(), exec, "org-123")
		if err == nil {
			t.Fatal("deleteTenantDataInTx() error = nil, want error")
		}

		if counts != nil {
			t.Fatalf("counts = %+v, want nil", counts)
		}

		if len(exec.queries) != 2 {
			t.Fatalf("queries = %d, want 2", len(exec.queries))
		}

		assertQueryContains(t, exec.queries[1], "DELETE FROM taxonomy_node_events")
	})

	t.Run("stops before feedback records after taxonomy runs delete error", func(t *testing.T) {
		// taxonomy_runs is the seventh delete (errAtQuery is 1-based).
		exec := &fakeTenantDataExecutor{tags: tenantDeleteTags(), errAtQuery: 7}

		counts, err := deleteTenantDataInTx(context.Background(), exec, "org-123")
		if err == nil {
			t.Fatal("deleteTenantDataInTx() error = nil, want error")
		}

		if counts != nil {
			t.Fatalf("counts = %+v, want nil", counts)
		}

		if len(exec.queries) != 7 {
			t.Fatalf("queries = %d, want 7", len(exec.queries))
		}

		assertQueryContains(t, exec.queries[6], "DELETE FROM taxonomy_runs")

		for _, query := range exec.queries {
			if strings.Contains(query, "DELETE FROM feedback_records") {
				t.Fatalf("feedback_records deleted before taxonomy runs error: %q", query)
			}
		}
	})

	t.Run("stops before webhooks after feedback delete error", func(t *testing.T) {
		// feedback_records is the eighth delete.
		exec := &fakeTenantDataExecutor{tags: tenantDeleteTags(), errAtQuery: 8}

		counts, err := deleteTenantDataInTx(context.Background(), exec, "org-123")
		if err == nil {
			t.Fatal("deleteTenantDataInTx() error = nil, want error")
		}

		if counts != nil {
			t.Fatalf("counts = %+v, want nil", counts)
		}

		if len(exec.queries) != 8 {
			t.Fatalf("queries = %d, want 8", len(exec.queries))
		}

		assertQueryContains(t, exec.queries[7], "DELETE FROM feedback_records")
	})

	t.Run("stops after tenant settings delete error", func(t *testing.T) {
		// tenant_settings is the tenth (final) delete.
		exec := &fakeTenantDataExecutor{tags: tenantDeleteTags(), errAtQuery: 10}

		counts, err := deleteTenantDataInTx(context.Background(), exec, "org-123")
		if err == nil {
			t.Fatal("deleteTenantDataInTx() error = nil, want error")
		}

		if counts != nil {
			t.Fatalf("counts = %+v, want nil", counts)
		}

		if len(exec.queries) != 10 {
			t.Fatalf("queries = %d, want 10", len(exec.queries))
		}

		assertQueryContains(t, exec.queries[9], "DELETE FROM tenant_settings")
	})
}

func assertQueryContains(t *testing.T, query, want string) {
	t.Helper()

	if !strings.Contains(query, want) {
		t.Fatalf("query %q does not contain %q", query, want)
	}
}
