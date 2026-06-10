package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeTenantDataDB struct {
	tx       *fakeTenantDataTx
	beginErr error
}

func (f *fakeTenantDataDB) BeginTx(context.Context, pgx.TxOptions) (tenantDataTx, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}

	return f.tx, nil
}

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

type fakeTenantDataTx struct {
	fakeTenantDataExecutor

	commitErr   error
	rollbackErr error
	committed   bool
	rolledBack  bool
}

func (f *fakeTenantDataTx) Commit(context.Context) error {
	f.committed = true

	return f.commitErr
}

func (f *fakeTenantDataTx) Rollback(context.Context) error {
	f.rolledBack = true

	return f.rollbackErr
}

func TestTenantDataRepository_DeleteByTenant(t *testing.T) {
	t.Run("commits transaction and returns counts", func(t *testing.T) {
		transaction := &fakeTenantDataTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				tags: []pgconn.CommandTag{
					pgconn.NewCommandTag("DELETE 2"),
					pgconn.NewCommandTag("DELETE 4"),
					pgconn.NewCommandTag("DELETE 3"),
					pgconn.NewCommandTag("DELETE 1"),
				},
			},
			rollbackErr: pgx.ErrTxClosed,
		}
		repo := &TenantDataRepository{db: &fakeTenantDataDB{tx: transaction}}

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
	})

	t.Run("rolls back and returns delete error", func(t *testing.T) {
		rollbackErr := errors.New("rollback failed")
		transaction := &fakeTenantDataTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{errAtQuery: 2},
			rollbackErr:            rollbackErr,
		}
		repo := &TenantDataRepository{db: &fakeTenantDataDB{tx: transaction}}

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
		repo := &TenantDataRepository{db: &fakeTenantDataDB{beginErr: beginErr}}

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
		transaction := &fakeTenantDataTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{
				tags: []pgconn.CommandTag{
					pgconn.NewCommandTag("DELETE 2"),
					pgconn.NewCommandTag("DELETE 4"),
					pgconn.NewCommandTag("DELETE 3"),
					pgconn.NewCommandTag("DELETE 1"),
				},
			},
			commitErr:   commitErr,
			rollbackErr: pgx.ErrTxClosed,
		}
		repo := &TenantDataRepository{db: &fakeTenantDataDB{tx: transaction}}

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
