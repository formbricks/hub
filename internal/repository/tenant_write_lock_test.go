package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/formbricks/hub/internal/huberrors"
)

type fakeTenantWriteRow struct {
	locked  bool
	scanErr error
}

func (r fakeTenantWriteRow) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}

	if len(dest) == 1 {
		if target, ok := dest[0].(*bool); ok {
			*target = r.locked
		}
	}

	return nil
}

type fakeTenantWriteTx struct {
	fakeTenantDataExecutor

	lockResults []bool
	lockScanErr error
	lockErrAt   int
	lockKeys    []string
	commitErr   error
	rollbackErr error
	committed   bool
	rolledBack  bool
}

func (f *fakeTenantWriteTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("query not implemented in fake")
}

func (f *fakeTenantWriteTx) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	if len(args) == 1 {
		if key, ok := args[0].(string); ok {
			f.lockKeys = append(f.lockKeys, key)
		}
	}

	if f.lockErrAt == len(f.lockKeys) && f.lockScanErr != nil {
		return fakeTenantWriteRow{scanErr: f.lockScanErr}
	}

	lockIndex := len(f.lockKeys) - 1
	if lockIndex < len(f.lockResults) {
		return fakeTenantWriteRow{locked: f.lockResults[lockIndex]}
	}

	return fakeTenantWriteRow{locked: true}
}

func (f *fakeTenantWriteTx) Commit(context.Context) error {
	f.committed = true

	return f.commitErr
}

func (f *fakeTenantWriteTx) Rollback(context.Context) error {
	f.rolledBack = true

	return f.rollbackErr
}

type fakeTenantWriteDB struct {
	tx       *fakeTenantWriteTx
	beginErr error
}

func (f *fakeTenantWriteDB) BeginTx(context.Context, pgx.TxOptions) (tenantWriteTx, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}

	return f.tx, nil
}

func TestTenantWriteLockKey(t *testing.T) {
	tests := []struct {
		tenantID string
		want     string
	}{
		{tenantID: "org-123", want: "tenant_write|7:org-123"},
		{tenantID: "", want: "tenant_write|0:"},
		{tenantID: "a|b", want: "tenant_write|3:a|b"},
	}

	for _, tt := range tests {
		if got := TenantWriteLockKey(tt.tenantID); got != tt.want {
			t.Errorf("TenantWriteLockKey(%q) = %q, want %q", tt.tenantID, got, tt.want)
		}
	}
}

func TestWithTenantWriteTx(t *testing.T) {
	t.Run("locks sorted deduplicated tenants, runs fn, commits", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{rollbackErr: pgx.ErrTxClosed}
		fnCalled := false

		err := withTenantWriteTx(
			context.Background(), &fakeTenantWriteDB{tx: transaction},
			[]string{"org-b", "org-a", "org-b"},
			func(tenantWriteTx) error {
				fnCalled = true

				return nil
			},
		)
		if err != nil {
			t.Fatalf("withTenantWriteTx() error = %v", err)
		}

		if !fnCalled {
			t.Fatal("fn was not called")
		}

		if !transaction.committed {
			t.Fatal("transaction was not committed")
		}

		wantKeys := []string{TenantWriteLockKey("org-a"), TenantWriteLockKey("org-b")}
		if len(transaction.lockKeys) != len(wantKeys) {
			t.Fatalf("lock keys = %v, want %v", transaction.lockKeys, wantKeys)
		}

		for keyIndex, want := range wantKeys {
			if transaction.lockKeys[keyIndex] != want {
				t.Fatalf("lock keys = %v, want %v", transaction.lockKeys, wantKeys)
			}
		}
	})

	t.Run("skips locking when no tenants given", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{rollbackErr: pgx.ErrTxClosed}

		err := withTenantWriteTx(
			context.Background(), &fakeTenantWriteDB{tx: transaction}, nil,
			func(tenantWriteTx) error { return nil },
		)
		if err != nil {
			t.Fatalf("withTenantWriteTx() error = %v", err)
		}

		if len(transaction.lockKeys) != 0 {
			t.Fatalf("lock keys = %v, want none", transaction.lockKeys)
		}
	})

	t.Run("unavailable lock returns conflict without calling fn", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{lockResults: []bool{false}}
		fnCalled := false

		err := withTenantWriteTx(
			context.Background(), &fakeTenantWriteDB{tx: transaction}, []string{"org-a"},
			func(tenantWriteTx) error {
				fnCalled = true

				return nil
			},
		)
		if !errors.Is(err, huberrors.ErrTenantWriteConflict) {
			t.Fatalf("withTenantWriteTx() error = %v, want tenant write conflict", err)
		}

		if fnCalled {
			t.Fatal("fn was called despite lock conflict")
		}

		if transaction.committed {
			t.Fatal("transaction was committed despite lock conflict")
		}

		if !transaction.rolledBack {
			t.Fatal("transaction was not rolled back")
		}
	})

	t.Run("lock scan error is wrapped", func(t *testing.T) {
		scanErr := errors.New("scan failed")
		transaction := &fakeTenantWriteTx{lockScanErr: scanErr, lockErrAt: 1}

		err := withTenantWriteTx(
			context.Background(), &fakeTenantWriteDB{tx: transaction}, []string{"org-a"},
			func(tenantWriteTx) error { return nil },
		)
		if !errors.Is(err, scanErr) {
			t.Fatalf("withTenantWriteTx() error = %v, want scan error", err)
		}

		if errors.Is(err, huberrors.ErrTenantWriteConflict) {
			t.Fatal("scan error must not be a tenant write conflict")
		}
	})

	t.Run("fn error rolls back without commit", func(t *testing.T) {
		fnErr := errors.New("fn failed")
		transaction := &fakeTenantWriteTx{}

		err := withTenantWriteTx(
			context.Background(), &fakeTenantWriteDB{tx: transaction}, []string{"org-a"},
			func(tenantWriteTx) error { return fnErr },
		)
		if !errors.Is(err, fnErr) {
			t.Fatalf("withTenantWriteTx() error = %v, want fn error", err)
		}

		if transaction.committed {
			t.Fatal("transaction was committed after fn error")
		}

		if !transaction.rolledBack {
			t.Fatal("transaction was not rolled back")
		}
	})

	t.Run("begin error is wrapped", func(t *testing.T) {
		beginErr := errors.New("begin failed")

		err := withTenantWriteTx(
			context.Background(), &fakeTenantWriteDB{beginErr: beginErr}, []string{"org-a"},
			func(tenantWriteTx) error { return nil },
		)
		if !errors.Is(err, beginErr) {
			t.Fatalf("withTenantWriteTx() error = %v, want begin error", err)
		}
	})

	t.Run("commit error is wrapped", func(t *testing.T) {
		commitErr := errors.New("commit failed")
		transaction := &fakeTenantWriteTx{commitErr: commitErr, rollbackErr: pgx.ErrTxClosed}

		err := withTenantWriteTx(
			context.Background(), &fakeTenantWriteDB{tx: transaction}, []string{"org-a"},
			func(tenantWriteTx) error { return nil },
		)
		if !errors.Is(err, commitErr) {
			t.Fatalf("withTenantWriteTx() error = %v, want commit error", err)
		}
	})
}

func TestAcquireTenantPurgeLock(t *testing.T) {
	t.Run("sets timeout, locks exclusively, resets timeout", func(t *testing.T) {
		exec := &fakeTenantDataExecutor{}

		err := acquireTenantPurgeLock(context.Background(), exec, "org-123", 5*time.Second)
		if err != nil {
			t.Fatalf("acquireTenantPurgeLock() error = %v", err)
		}

		if len(exec.queries) != 3 {
			t.Fatalf("queries = %d, want 3", len(exec.queries))
		}

		assertQueryContains(t, exec.queries[0], "set_config('lock_timeout', $1, true)")
		assertQueryContains(t, exec.queries[1], "pg_advisory_xact_lock(hashtextextended($1, 0))")
		assertQueryContains(t, exec.queries[2], "set_config('lock_timeout', '0', true)")

		if len(exec.args[0]) != 1 || exec.args[0][0] != "5000" {
			t.Fatalf("set_config args = %#v, want timeout in milliseconds", exec.args[0])
		}

		if len(exec.args[1]) != 1 || exec.args[1][0] != TenantWriteLockKey("org-123") {
			t.Fatalf("lock args = %#v, want tenant write lock key", exec.args[1])
		}
	})

	t.Run("floors non-positive timeout to 1ms so lock_timeout is never disabled", func(t *testing.T) {
		// lock_timeout = 0 means "wait forever" in Postgres; a zero or negative
		// duration must never reach set_config as "0".
		for _, timeout := range []time.Duration{0, -5 * time.Second, time.Microsecond} {
			exec := &fakeTenantDataExecutor{}

			if err := acquireTenantPurgeLock(context.Background(), exec, "org-123", timeout); err != nil {
				t.Fatalf("acquireTenantPurgeLock(%v) error = %v", timeout, err)
			}

			if len(exec.args[0]) != 1 || exec.args[0][0] != "1" {
				t.Fatalf("set_config args for timeout %v = %#v, want floored to \"1\"", timeout, exec.args[0])
			}
		}
	})

	t.Run("lock timeout maps to tenant write conflict", func(t *testing.T) {
		exec := &fakeTenantDataExecutor{
			errAtQuery: 2,
			err:        &pgconn.PgError{Code: lockNotAvailableSQLState},
		}

		err := acquireTenantPurgeLock(context.Background(), exec, "org-123", time.Second)
		if !errors.Is(err, huberrors.ErrTenantWriteConflict) {
			t.Fatalf("acquireTenantPurgeLock() error = %v, want tenant write conflict", err)
		}
	})

	t.Run("other lock error is wrapped, not conflict", func(t *testing.T) {
		lockErr := errors.New("connection lost")
		exec := &fakeTenantDataExecutor{errAtQuery: 2, err: lockErr}

		err := acquireTenantPurgeLock(context.Background(), exec, "org-123", time.Second)
		if !errors.Is(err, lockErr) {
			t.Fatalf("acquireTenantPurgeLock() error = %v, want lock error", err)
		}

		if errors.Is(err, huberrors.ErrTenantWriteConflict) {
			t.Fatal("generic lock error must not be a tenant write conflict")
		}
	})

	t.Run("set_config error is wrapped", func(t *testing.T) {
		setErr := errors.New("set_config failed")
		exec := &fakeTenantDataExecutor{errAtQuery: 1, err: setErr}

		err := acquireTenantPurgeLock(context.Background(), exec, "org-123", time.Second)
		if !errors.Is(err, setErr) {
			t.Fatalf("acquireTenantPurgeLock() error = %v, want set_config error", err)
		}
	})
}
