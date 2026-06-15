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

func TestTenantDataRepository_DeleteByTenant(t *testing.T) {
	t.Run("locks tenant exclusively, commits transaction, and returns counts", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				tags: append(purgeLockTags(),
					pgconn.NewCommandTag("DELETE 2"),
					pgconn.NewCommandTag("DELETE 4"),
					pgconn.NewCommandTag("DELETE 3"),
					pgconn.NewCommandTag("DELETE 1"),
				),
			},
			rollbackErr: pgx.ErrTxClosed,
		}
		repo := &TenantDataRepository{db: &fakeTenantWriteDB{tx: transaction}, purgeLockTimeout: 5 * time.Second}

		counts, err := repo.DeleteByTenant(context.Background(), "org-123")
		if err != nil {
			t.Fatalf("DeleteByTenant() error = %v", err)
		}

		if counts.DeletedEmbeddings != 2 || counts.DeletedFeedbackRecords != 3 || counts.DeletedWebhooks != 1 {
			t.Fatalf("counts = %+v, want embeddings=2 feedback=3 webhooks=1", counts)
		}

		if !transaction.committed {
			t.Fatal("transaction was not committed")
		}

		if !transaction.rolledBack {
			t.Fatal("deferred rollback was not called")
		}

		if len(transaction.queries) != 7 {
			t.Fatalf("queries = %d, want 7 (3 lock statements + 4 deletes)", len(transaction.queries))
		}

		assertQueryContains(t, transaction.queries[0], "set_config('lock_timeout', $1, true)")
		assertQueryContains(t, transaction.queries[1], "pg_advisory_xact_lock(hashtextextended($1, 0))")
		assertQueryContains(t, transaction.queries[2], "set_config('lock_timeout', '0', true)")
		assertQueryContains(t, transaction.queries[3], "DELETE FROM embeddings")

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
				tags: append(purgeLockTags(),
					pgconn.NewCommandTag("DELETE 2"),
					pgconn.NewCommandTag("DELETE 4"),
					pgconn.NewCommandTag("DELETE 3"),
					pgconn.NewCommandTag("DELETE 1"),
				),
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
		exec := &fakeTenantDataExecutor{
			tags: []pgconn.CommandTag{
				pgconn.NewCommandTag("DELETE 2"),
				pgconn.NewCommandTag("DELETE 4"),
				pgconn.NewCommandTag("DELETE 3"),
				pgconn.NewCommandTag("DELETE 1"),
			},
		}

		counts, err := deleteTenantDataInTx(context.Background(), exec, "org-123")
		if err != nil {
			t.Fatalf("deleteTenantDataInTx() error = %v", err)
		}

		if counts.DeletedEmbeddings != 2 || counts.DeletedFeedbackRecords != 3 || counts.DeletedWebhooks != 1 {
			t.Fatalf("counts = %+v, want embeddings=2 feedback=3 webhooks=1", counts)
		}

		if len(exec.queries) != 4 {
			t.Fatalf("queries = %d, want 4", len(exec.queries))
		}

		assertQueryContains(t, exec.queries[0], "DELETE FROM embeddings")
		assertQueryContains(t, exec.queries[0], "USING feedback_records")
		assertQueryContains(t, exec.queries[1], "DELETE FROM taxonomy_runs")
		assertQueryContains(t, exec.queries[2], "DELETE FROM feedback_records")
		assertQueryContains(t, exec.queries[3], "DELETE FROM webhooks")

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

	t.Run("stops before feedback records after taxonomy runs delete error", func(t *testing.T) {
		exec := &fakeTenantDataExecutor{
			tags: []pgconn.CommandTag{
				pgconn.NewCommandTag("DELETE 2"),
			},
			errAtQuery: 2,
		}

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
	})

	t.Run("stops before webhooks after feedback delete error", func(t *testing.T) {
		exec := &fakeTenantDataExecutor{
			tags: []pgconn.CommandTag{
				pgconn.NewCommandTag("DELETE 2"),
				pgconn.NewCommandTag("DELETE 4"),
			},
			errAtQuery: 3,
		}

		counts, err := deleteTenantDataInTx(context.Background(), exec, "org-123")
		if err == nil {
			t.Fatal("deleteTenantDataInTx() error = nil, want error")
		}

		if counts != nil {
			t.Fatalf("counts = %+v, want nil", counts)
		}

		if len(exec.queries) != 3 {
			t.Fatalf("queries = %d, want 3", len(exec.queries))
		}
	})
}

func assertQueryContains(t *testing.T, query, want string) {
	t.Helper()

	if !strings.Contains(query, want) {
		t.Fatalf("query %q does not contain %q", query, want)
	}
}
