package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/formbricks/hub/internal/huberrors"
)

// Tenant write serialization.
//
// Every tenant-owned mutation runs inside a transaction that first acquires a
// SHARED transaction-scoped advisory lock on the tenant's key, so concurrent
// writes for the same tenant proceed in parallel. The tenant data purge
// acquires the same key EXCLUSIVELY, so a purge and tenant-owned writes for
// the same tenant are mutually exclusive while other tenants are unaffected.
// Writers use try-locks (fail fast with a retryable conflict, never wait);
// the purge blocks for a bounded time. Postgres queues no new shared
// try-locks behind a waiting exclusive request, so writers are rejected from
// the moment a purge arrives while in-flight writers drain.
//
// Lock-order convention (deadlock-free by construction): tenant shared
// try-lock first, then any scope-specific advisory lock (e.g. taxonomy run
// scope), then row locks. The purge takes only the tenant exclusive lock and
// holds no row locks while waiting. No code path may block on an advisory
// lock while holding row locks.
//
// Two write shapes:
//   - Tenant known before the write (inserts): gate the write on the lock in a
//     single autocommit statement via tenantWriteLockGate — one round trip, no
//     explicit transaction.
//   - Tenant resolved from the row being mutated (updates/deletes): use
//     withTenantWriteTx / withTenantWritePoolTx, resolve the tenant inside the
//     transaction, then tryLockTenantsShared before mutating.

// tenantWriteLockSharedSQL acquires the tenant key in shared mode without waiting.
const tenantWriteLockSharedSQL = `SELECT pg_try_advisory_xact_lock_shared(hashtextextended($1, 0))`

// tenantWriteLockExclusiveSQL acquires the tenant key exclusively, waiting up
// to the transaction-local lock_timeout.
const tenantWriteLockExclusiveSQL = `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`

// lockNotAvailableSQLState is the SQLSTATE Postgres reports when lock_timeout
// expires while waiting for a lock (55P03 lock_not_available).
const lockNotAvailableSQLState = "55P03"

// tenantWriteLockGate returns a SQL boolean expression that try-acquires the
// shared tenant write lock, for gating a single-statement write
// (INSERT ... SELECT ... WHERE <gate>) without an explicit transaction. Use it
// only when the tenant is known before the write (no in-transaction resolve):
// the advisory lock is transaction-scoped, and in autocommit the transaction is
// the single statement, so the lock is held for exactly the duration of the
// write — the same isolation as the explicit-transaction path, in one round
// trip. The statement must pass TenantWriteLockKey(tenantID) as the $paramIndex
// parameter, and the caller must treat zero affected rows (pgx.ErrNoRows) as a
// tenant write conflict (the lock was refused: a purge is in progress).
func tenantWriteLockGate(paramIndex int) string {
	return fmt.Sprintf("pg_try_advisory_xact_lock_shared(hashtextextended($%d, 0))", paramIndex)
}

// TenantWriteLockKey returns the advisory lock key string that serializes
// tenant-owned writes against tenant data purges for the given tenant.
// Format: "tenant_write|<len>:<tenant_id>" (length-prefixed to keep distinct
// tenant IDs from aliasing); the key is hashed in SQL via
// hashtextextended(key, 0). Exported because the format is a cross-process
// contract: the API, the worker, and integration tests must compute the
// identical key to coordinate on the same lock.
func TenantWriteLockKey(tenantID string) string {
	return "tenant_write|" + strconv.Itoa(len(tenantID)) + ":" + tenantID
}

// tenantWriteTx is the transaction surface tenant-owned mutations run on.
// pgx.Tx satisfies it.
type tenantWriteTx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// tenantWriteTxBeginner abstracts transaction creation so the helper is
// testable without a database.
type tenantWriteTxBeginner interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (tenantWriteTx, error)
}

// tenantWritePool adapts *pgxpool.Pool to tenantWriteTxBeginner. It is the
// transaction source for every tenant-owned write path, including the tenant
// data purge.
type tenantWritePool struct {
	db *pgxpool.Pool
}

func (p tenantWritePool) BeginTx(ctx context.Context, txOptions pgx.TxOptions) (tenantWriteTx, error) {
	dbTx, err := p.db.BeginTx(ctx, txOptions)
	if err != nil {
		return nil, fmt.Errorf("begin tenant write transaction: %w", err)
	}

	return dbTx, nil
}

// withTenantWritePoolTx is withTenantWriteTx for the common case of a
// *pgxpool.Pool, so call sites do not repeat the tenantWritePool adapter.
func withTenantWritePoolTx(
	ctx context.Context, pool *pgxpool.Pool, tenantIDs []string, mutate func(dbTx tenantWriteTx) error,
) error {
	return withTenantWriteTx(ctx, tenantWritePool{db: pool}, tenantIDs, mutate)
}

// withTenantWriteTx runs mutate inside a transaction that holds shared tenant
// write locks for the given tenant IDs (sorted and deduplicated). Pass no
// tenant IDs when the tenant boundary can only be resolved inside the
// transaction; mutate must then call tryLockTenantsShared itself before
// writing. A lock that cannot be acquired immediately aborts with
// *huberrors.TenantWriteConflictError without calling mutate.
func withTenantWriteTx(
	ctx context.Context, db tenantWriteTxBeginner, tenantIDs []string, mutate func(dbTx tenantWriteTx) error,
) error {
	dbTx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tenant write transaction: %w", err)
	}

	defer func() {
		if err := dbTx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Error("tenant write tx: rollback failed", "error", err)
		}
	}()

	if err := tryLockTenantsShared(ctx, dbTx, tenantIDs); err != nil {
		return err
	}

	if err := mutate(dbTx); err != nil {
		return err
	}

	if err := dbTx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tenant write transaction: %w", err)
	}

	return nil
}

// tryLockTenantsShared acquires the shared tenant write lock for every given
// tenant ID without waiting. It returns *huberrors.TenantWriteConflictError
// when any lock is unavailable, which means a tenant data purge for that tenant
// is running or waiting to run.
func tryLockTenantsShared(ctx context.Context, querier queryer, tenantIDs []string) error {
	// Fast path for the common single-tenant write: skip the clone/sort/dedup
	// allocation entirely.
	switch len(tenantIDs) {
	case 0:
		return nil
	case 1:
		return tryLockTenantShared(ctx, querier, tenantIDs[0])
	}

	// Multiple tenants (GDPR erasure across tenants, webhook tenant-move):
	// deduplicate so a repeated tenant_id does not cost a redundant lock round
	// trip. Try-locks never wait, so the order is not needed for deadlock
	// avoidance; sorting just keeps behavior deterministic.
	sorted := slices.Clone(tenantIDs)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)

	for _, tenantID := range sorted {
		if err := tryLockTenantShared(ctx, querier, tenantID); err != nil {
			return err
		}
	}

	return nil
}

func tryLockTenantShared(ctx context.Context, querier queryer, tenantID string) error {
	var locked bool
	if err := querier.QueryRow(ctx, tenantWriteLockSharedSQL, TenantWriteLockKey(tenantID)).Scan(&locked); err != nil {
		return fmt.Errorf("acquire shared tenant write lock: %w", err)
	}

	if !locked {
		return huberrors.NewTenantWriteConflictError("tenant data purge in progress for this tenant; retry later")
	}

	return nil
}

// acquireTenantPurgeLock acquires the tenant write lock exclusively, waiting
// up to timeout for in-flight tenant-owned writes to drain. On success the
// transaction-local lock_timeout is reset to 0 so it does not apply to the
// purge's subsequent row and FK lock acquisitions. When the timeout expires
// it returns *huberrors.TenantWriteConflictError (retryable).
func acquireTenantPurgeLock(
	ctx context.Context, exec tenantDataExecutor, tenantID string, timeout time.Duration,
) error {
	// Postgres treats lock_timeout = 0 as "wait forever"; floor a zero or
	// sub-millisecond duration to 1ms so the purge always fails fast rather
	// than blocking indefinitely on in-flight writers.
	timeoutMs := max(timeout.Milliseconds(), 1)
	if _, err := exec.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`, strconv.FormatInt(timeoutMs, 10)); err != nil {
		return fmt.Errorf("set tenant purge lock timeout: %w", err)
	}

	if _, err := exec.Exec(ctx, tenantWriteLockExclusiveSQL, TenantWriteLockKey(tenantID)); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == lockNotAvailableSQLState {
			return huberrors.NewTenantWriteConflictError("tenant-owned writes in progress; retry purge later")
		}

		return fmt.Errorf("acquire exclusive tenant purge lock: %w", err)
	}

	if _, err := exec.Exec(ctx, `SELECT set_config('lock_timeout', '0', true)`); err != nil {
		return fmt.Errorf("reset tenant purge lock timeout: %w", err)
	}

	return nil
}
