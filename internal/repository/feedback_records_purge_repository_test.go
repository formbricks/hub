package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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
	highWaterMark := uuid.New()

	return &fakeTenantWriteTx{
		fakeTenantDataExecutor: fakeTenantDataExecutor{tags: purgeLockTags()},
		highWaterMark:          &highWaterMark,
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

		if counts.ClusterMemberships != 308 {
			t.Fatalf("DeletedTaxonomyClusterMemberships = %d, want 308", counts.ClusterMemberships)
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

		// One per record batch (2 here, the second returning zero) plus one for the final taxonomy
		// transaction.
		if lockAcquisitions != 3 {
			t.Fatalf("exclusive lock taken %d times, want one per batch plus the taxonomy pass (3)", lockAcquisitions)
		}
	})

	// The line this purge draws: it takes the data AND the taxonomy built on that data, but never the
	// tenant's configuration. Deleting webhooks would destroy integrator setup (including signing keys
	// that reads never return); deleting tenant_settings would silently revert enrichment opt-outs to
	// enabled. Both are the offboarding purge's job, not this one's.
	t.Run("takes the records and the taxonomy but never the configuration", func(t *testing.T) {
		transaction := purgeBatches([]int64{10, 5, 3}, []int64{0, 0, 0})

		if _, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123"); err != nil {
			t.Fatalf("PurgeFeedbackRecordsByTenant() error = %v", err)
		}

		statements := strings.Join(append(append([]string{}, transaction.purgeBatchQueries...), transaction.queries...), "\n")

		for _, table := range []string{"webhooks", "tenant_settings"} {
			if strings.Contains(statements, "DELETE FROM "+table) {
				t.Fatalf("purge deleted from %q; that is tenant configuration, not tenant data", table)
			}
		}

		for _, want := range []string{
			"DELETE FROM embeddings",
			"DELETE FROM feedback_records",
			"DELETE FROM taxonomy_cluster_memberships",
			"DELETE FROM taxonomy_node_events",
			"DELETE FROM taxonomy_nodes",
			"DELETE FROM taxonomy_clusters",
			"DELETE FROM taxonomy_active_runs",
			"DELETE FROM taxonomy_runs",
		} {
			if !strings.Contains(statements, want) {
				t.Fatalf("purge statement missing %q", want)
			}
		}
	})

	// The taxonomy goes last: a purge interrupted midway should leave records missing but the tree
	// standing, never a tree describing records that are already gone. Asserted against the single
	// ordered statement log — comparing the per-phase slices would pass with the phases swapped.
	t.Run("removes the taxonomy only after the records", func(t *testing.T) {
		transaction := purgeBatches([]int64{10, 0, 0}, []int64{0, 0, 0})

		if _, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123"); err != nil {
			t.Fatalf("PurgeFeedbackRecordsByTenant() error = %v", err)
		}

		lastBatch, firstTaxonomy := -1, -1

		for i, statement := range transaction.statements {
			if strings.Contains(statement, "WITH victims") {
				lastBatch = i
			}

			if firstTaxonomy == -1 && strings.Contains(statement, "DELETE FROM taxonomy_runs") {
				firstTaxonomy = i
			}
		}

		if lastBatch == -1 {
			t.Fatal("no record batches ran")
		}

		if firstTaxonomy == -1 {
			t.Fatal("taxonomy was never removed")
		}

		if firstTaxonomy < lastBatch {
			t.Fatalf("taxonomy removed at statement %d, before the last record batch at %d",
				firstTaxonomy, lastBatch)
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

		if !strings.Contains(statement, "LIMIT $3") {
			t.Fatalf("batch is not bounded: %s", statement)
		}

		if !strings.Contains(statement, "id <= $2") {
			t.Fatalf("batch is not bounded to the records that existed at purge start: %s", statement)
		}

		args := transaction.purgeBatchArgs[0]
		if len(args) != 3 || args[0] != "org-123" || args[2] != feedbackRecordsPurgeBatchSize {
			t.Fatalf("batch args = %#v, want tenant + high-water mark + batch size", args)
		}

		if args[1] != *transaction.highWaterMark {
			t.Fatalf("batch high-water mark = %v, want the ceiling read at purge start", args[1])
		}
	})

	// Without a ceiling, a tenant that keeps ingesting keeps feeding the loop: the purge might never
	// reach a zero batch, and it would delete feedback that arrived after the operator asked to empty
	// the dataset.
	t.Run("reads the ceiling once, before any batch", func(t *testing.T) {
		transaction := purgeBatches([]int64{10, 0, 0}, []int64{0, 0, 0})

		if _, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123"); err != nil {
			t.Fatalf("PurgeFeedbackRecordsByTenant() error = %v", err)
		}

		if transaction.highWaterMarkReads != 1 {
			t.Fatalf("high-water mark read %d times, want exactly once", transaction.highWaterMarkReads)
		}

		for _, args := range transaction.purgeBatchArgs {
			if args[1] != *transaction.highWaterMark {
				t.Fatalf("a batch used a different ceiling: %v", args[1])
			}
		}
	})

	// A tenant with no records at all must not run a batch, but must still clear any taxonomy left
	// behind by an earlier run.
	t.Run("skips the record loop when the tenant has none", func(t *testing.T) {
		transaction := &fakeTenantWriteTx{
			fakeTenantDataExecutor: fakeTenantDataExecutor{tags: purgeLockTags()},
			rollbackErr:            pgx.ErrTxClosed,
		}

		counts, err := newRecordsPurgeRepo(transaction).
			PurgeFeedbackRecordsByTenant(context.Background(), "org-123")
		if err != nil {
			t.Fatalf("PurgeFeedbackRecordsByTenant() error = %v", err)
		}

		if len(transaction.purgeBatchQueries) != 0 {
			t.Fatalf("ran %d batches for a tenant with no records", len(transaction.purgeBatchQueries))
		}

		if counts.DeletedFeedbackRecords != 0 {
			t.Fatalf("DeletedFeedbackRecords = %d, want 0", counts.DeletedFeedbackRecords)
		}

		taxonomyRan := false

		for _, query := range transaction.queries {
			if strings.Contains(query, "DELETE FROM taxonomy_runs") {
				taxonomyRan = true
			}
		}

		if !taxonomyRan {
			t.Fatal("taxonomy was not cleared for a tenant with no records")
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
