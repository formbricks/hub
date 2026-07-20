package tests

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
)

// cleanupTaxonomyTenant removes all taxonomy runs (cascading their artifacts) and feedback
// records for a tenant once the test finishes, keeping the shared test database tidy. It
// reuses the caller's context (the test bodies use context.Background(), which is never
// canceled) so the cleanup still runs after the test returns.
func cleanupTaxonomyTenant(ctx context.Context, t *testing.T, db *pgxpool.Pool, tenantID string) {
	t.Helper()

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM taxonomy_runs WHERE tenant_id = $1`, tenantID)
		_, _ = db.Exec(ctx, `DELETE FROM feedback_records WHERE tenant_id = $1`, tenantID)
	})
}

// insertScopeFeedbackRecord inserts a single text feedback record for a scope and returns
// its ID, so membership rows in StoreResultAndActivate reference a real feedback record.
func insertScopeFeedbackRecord(ctx context.Context, t *testing.T, db *pgxpool.Pool, scope models.TaxonomyScope) uuid.UUID {
	t.Helper()

	var id uuid.UUID

	err := db.QueryRow(ctx, `
		INSERT INTO feedback_records (
			source_type, source_id, field_id, field_label, field_type,
			value_text, tenant_id, submission_id
		)
		VALUES ($1, NULLIF($2, ''), $3, 'Feedback', 'text'::field_type_enum, $4, $5, $6)
		RETURNING id`,
		scope.SourceType, scope.SourceID, scope.FieldID,
		"Login was confusing", scope.TenantID, "submission-"+uuid.NewString(),
	).Scan(&id)
	require.NoError(t, err)

	return id
}

// TestTaxonomyRepository_RunLifecycle covers the run state machine: create/reuse, the
// pending->running transition, and the terminal failed transition, including the conflicts
// that guard illegal transitions.
func TestTaxonomyRepository_RunLifecycle(t *testing.T) {
	ctx := context.Background()
	db := taxonomyTestDB(t)
	repo := repository.NewTaxonomyRepository(db)

	t.Run("create persists a pending run and a second call reuses it", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-create")
		cleanupTaxonomyTenant(ctx, t, db, scope.TenantID)

		run, created, err := repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{
			TaxonomyScope: scope, RecordCount: 5, EmbeddingCount: 5,
		})
		require.NoError(t, err)
		require.True(t, created)
		require.Equal(t, models.TaxonomyRunStatusPending, run.Status)
		requireUUIDv7(t, run.ID)

		reused, createdAgain, err := repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{
			TaxonomyScope: scope, RecordCount: 5, EmbeddingCount: 5,
		})
		require.NoError(t, err)
		require.False(t, createdAgain, "an in-progress run must be reused, not duplicated")
		require.Equal(t, run.ID, reused.ID)
	})

	t.Run("mark running transitions pending to running and rejects a repeat", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-running")
		cleanupTaxonomyTenant(ctx, t, db, scope.TenantID)

		run, _, err := repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{TaxonomyScope: scope})
		require.NoError(t, err)

		running, err := repo.MarkRunRunning(ctx, run.ID, scope.TenantID)
		require.NoError(t, err)
		require.Equal(t, models.TaxonomyRunStatusRunning, running.Status)
		require.NotNil(t, running.StartedAt)

		_, err = repo.MarkRunRunning(ctx, run.ID, scope.TenantID)
		require.ErrorIs(t, err, huberrors.ErrConflict, "running->running must conflict")
	})

	t.Run("mark failed records error and code and rejects a terminal run", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-failed")
		cleanupTaxonomyTenant(ctx, t, db, scope.TenantID)

		run, _, err := repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{TaxonomyScope: scope})
		require.NoError(t, err)

		failed, err := repo.MarkRunFailed(
			ctx, run.ID, scope.TenantID, "clustering failed", models.TaxonomyRunFailureCodeInsufficientData,
		)
		require.NoError(t, err)
		require.Equal(t, models.TaxonomyRunStatusFailed, failed.Status)
		require.NotNil(t, failed.Error)
		require.Equal(t, "clustering failed", *failed.Error)
		require.NotNil(t, failed.ErrorCode)
		require.Equal(t, models.TaxonomyRunFailureCodeInsufficientData, *failed.ErrorCode)
		require.NotNil(t, failed.FinishedAt)

		_, err = repo.MarkRunFailed(
			ctx, run.ID, scope.TenantID, "again", models.TaxonomyRunFailureCodeInternalError,
		)
		require.ErrorIs(t, err, huberrors.ErrConflict, "failed->failed must conflict")
	})

	t.Run("unknown run id is not found", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-missing")
		_, err := repo.MarkRunRunning(ctx, uuid.New(), scope.TenantID)
		require.ErrorIs(t, err, huberrors.ErrNotFound)
	})
}

// TestTaxonomyRepository_FailStuckRuns covers the reaper sweep: runs left in running (via started_at)
// or pending (via created_at) past the timeout are transitioned to failed with the internal_error
// code, while a run newer than the timeout is left untouched. The sweep is cross-tenant, so the test
// asserts on the specific runs rather than an exact count.
func TestTaxonomyRepository_FailStuckRuns(t *testing.T) {
	ctx := context.Background()
	db := taxonomyTestDB(t)
	repo := repository.NewTaxonomyRepository(db)

	stuckScope := uniqueTaxonomyScope("tax-reap-stuck")
	pendingScope := uniqueTaxonomyScope("tax-reap-pending")
	freshScope := uniqueTaxonomyScope("tax-reap-fresh")

	for _, s := range []models.TaxonomyScope{stuckScope, pendingScope, freshScope} {
		cleanupTaxonomyTenant(ctx, t, db, s.TenantID)
	}

	// A run orphaned in `running`: started_at is old, so COALESCE(started_at, created_at) is old.
	stuck, _, err := repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{TaxonomyScope: stuckScope})
	require.NoError(t, err)
	_, err = repo.MarkRunRunning(ctx, stuck.ID, stuckScope.TenantID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE taxonomy_runs SET started_at = NOW() - INTERVAL '2 hours' WHERE id = $1`, stuck.ID)
	require.NoError(t, err)

	// A run orphaned in `pending`: started_at is NULL, so the reaper falls back to created_at.
	pending, _, err := repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{TaxonomyScope: pendingScope})
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE taxonomy_runs SET created_at = NOW() - INTERVAL '2 hours' WHERE id = $1`, pending.ID)
	require.NoError(t, err)

	// A fresh run that must survive the sweep.
	fresh, _, err := repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{TaxonomyScope: freshScope})
	require.NoError(t, err)
	_, err = repo.MarkRunRunning(ctx, fresh.ID, freshScope.TenantID)
	require.NoError(t, err)

	failed, err := repo.FailStuckRuns(ctx, time.Hour, "stuck run", models.TaxonomyRunFailureCodeInternalError)
	require.NoError(t, err)
	require.GreaterOrEqual(t, failed, int64(2), "both orphaned runs should be reaped")

	for _, id := range []uuid.UUID{stuck.ID, pending.ID} {
		reaped, err := repo.GetRunForInternalService(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, models.TaxonomyRunStatusFailed, reaped.Status)
		require.NotNil(t, reaped.ErrorCode)
		assert.Equal(t, models.TaxonomyRunFailureCodeInternalError, *reaped.ErrorCode)
		assert.NotNil(t, reaped.FinishedAt)
	}

	survivor, err := repo.GetRunForInternalService(ctx, fresh.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaxonomyRunStatusRunning, survivor.Status, "a run newer than the timeout must not be reaped")
}

// TestTaxonomyRepository_StoreResultAndActivate covers persisting the full artifact graph
// (clusters, memberships, nodes), activating the run, replacing a prior active run, and the
// conflict when the run is not in the running state.
func TestTaxonomyRepository_StoreResultAndActivate(t *testing.T) {
	ctx := context.Background()
	db := taxonomyTestDB(t)
	repo := repository.NewTaxonomyRepository(db)

	scope := uniqueTaxonomyScope("tax-store")
	cleanupTaxonomyTenant(ctx, t, db, scope.TenantID)

	feedbackRecordID := insertScopeFeedbackRecord(ctx, t, db, scope)

	result := models.TaxonomyRunResultRequest{
		Clusters: []models.TaxonomyResultCluster{
			{ClusterKey: 1, Label: new("login"), LLMLabel: new("Login issues"), Size: 1},
		},
		Memberships: []models.TaxonomyResultMembership{
			{ClusterKey: 1, FeedbackRecordID: feedbackRecordID, Confidence: new(0.9)},
		},
		Nodes: []models.TaxonomyResultNode{
			{NodeKey: "root", NodeType: models.TaxonomyNodeTypeRoot, Label: "Feedback", Level: 0},
			{NodeKey: "branch", ParentKey: new("root"), NodeType: models.TaxonomyNodeTypeBranch, Label: "Access", Level: 1},
			{NodeKey: "leaf", ParentKey: new("branch"), ClusterKey: new(1), NodeType: models.TaxonomyNodeTypeLeaf, Label: "Login", Level: 2},
		},
	}

	runReadyToStore := func(t *testing.T) uuid.UUID {
		t.Helper()

		run, _, err := repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{
			TaxonomyScope: scope, RecordCount: 1, EmbeddingCount: 1,
		})
		require.NoError(t, err)

		_, err = repo.MarkRunRunning(ctx, run.ID, scope.TenantID)
		require.NoError(t, err)

		return run.ID
	}

	firstRunID := runReadyToStore(t)

	stored, err := repo.StoreResultAndActivate(ctx, firstRunID, scope.TenantID, result)
	require.NoError(t, err)
	require.Equal(t, models.TaxonomyRunStatusSucceeded, stored.Status)
	require.Equal(t, 1, stored.ClusterCount)
	require.Equal(t, 3, stored.NodeCount)
	require.NotNil(t, stored.FinishedAt)

	// Every artifact table is populated for the run.
	assert.Equal(t, int64(1), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_clusters WHERE run_id = $1`, firstRunID))
	assert.Equal(t, int64(1),
		countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_cluster_memberships WHERE run_id = $1`, firstRunID))
	assert.Equal(t, int64(3), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_nodes WHERE run_id = $1`, firstRunID))

	// The run is now the active run for its scope.
	active, err := repo.GetActiveRun(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, firstRunID, active.ID)

	// A second completed run for the same scope replaces the active pointer (upsert).
	secondRunID := runReadyToStore(t)
	_, err = repo.StoreResultAndActivate(ctx, secondRunID, scope.TenantID, result)
	require.NoError(t, err)

	active, err = repo.GetActiveRun(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, secondRunID, active.ID, "activating a new run must replace the previous active run")
	assert.Equal(t, int64(1),
		countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_active_runs WHERE tenant_id = $1`, scope.TenantID))

	// Storing a result for a run that is not running conflicts (it is already succeeded).
	_, err = repo.StoreResultAndActivate(ctx, firstRunID, scope.TenantID, result)
	require.ErrorIs(t, err, huberrors.ErrConflict)
}

// TestTaxonomyRepository_RenameAndRemoveNode covers node edits, the audit events they emit,
// and soft-remove visibility in the tree.
func TestTaxonomyRepository_RenameAndRemoveNode(t *testing.T) {
	ctx := context.Background()
	db := taxonomyTestDB(t)
	repo := repository.NewTaxonomyRepository(db)

	scope := uniqueTaxonomyScope("tax-edit")
	ids := seedTaxonomyGraph(ctx, t, db, scope)

	t.Run("rename updates the label and records a rename event", func(t *testing.T) {
		renamed, err := repo.RenameNode(ctx, ids.BranchID, scope.TenantID, "actor-rename", "Account Access")
		require.NoError(t, err)
		require.Equal(t, "Account Access", renamed.Label)

		events := countTenantDataRows(ctx, t, db, `
			SELECT COUNT(*) FROM taxonomy_node_events
			WHERE node_id = $1 AND event_type = 'rename' AND actor_id = 'actor-rename'
				AND new_value->>'label' = 'Account Access'`, ids.BranchID)
		assert.Equal(t, int64(1), events, "a rename must record exactly one rename event")
	})

	t.Run("soft-remove sets removed metadata, records an event, and hides the node", func(t *testing.T) {
		removed, err := repo.RemoveNode(ctx, ids.LeafID, scope.TenantID, "actor-remove")
		require.NoError(t, err)
		require.NotNil(t, removed.RemovedAt)
		require.NotNil(t, removed.RemovedBy)
		require.Equal(t, "actor-remove", *removed.RemovedBy)

		events := countTenantDataRows(ctx, t, db, `
			SELECT COUNT(*) FROM taxonomy_node_events
			WHERE node_id = $1 AND event_type = 'soft_remove' AND actor_id = 'actor-remove'`, ids.LeafID)
		assert.Equal(t, int64(1), events)

		tree, err := repo.GetTree(ctx, ids.RunID, scope.TenantID)
		require.NoError(t, err)
		require.NotNil(t, tree.Root)
		require.False(t, treeContainsNode(tree.Root, ids.LeafID), "a soft-removed node must not appear in the tree")
		require.True(t, treeContainsNode(tree.Root, ids.BranchID), "non-removed nodes must remain visible")
	})
}

// treeContainsNode reports whether nodeID appears anywhere in the visible tree.
func treeContainsNode(node *models.TaxonomyNode, nodeID uuid.UUID) bool {
	if node == nil {
		return false
	}

	if node.ID == nodeID {
		return true
	}

	for i := range node.Children {
		if treeContainsNode(&node.Children[i], nodeID) {
			return true
		}
	}

	return false
}

// TestTaxonomyRepository_ListNodeRecords covers the recursive drilldown from a node to the
// feedback records assigned to it and its descendants.
func TestTaxonomyRepository_ListNodeRecords(t *testing.T) {
	ctx := context.Background()
	db := taxonomyTestDB(t)
	repo := repository.NewTaxonomyRepository(db)

	scope := uniqueTaxonomyScope("tax-noderecords")
	ids := seedTaxonomyGraph(ctx, t, db, scope)

	// The root aggregates records from its descendant leaf's cluster membership.
	records, _, err := repo.ListNodeRecords(ctx, ids.RootID, scope.TenantID, 50)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, ids.FeedbackRecordID, records[0].ID)

	// A different tenant sees nothing for the same node id.
	otherTenantRecords, _, err := repo.ListNodeRecords(ctx, ids.RootID, "other-tenant-"+uuid.NewString(), 50)
	require.NoError(t, err)
	require.Empty(t, otherTenantRecords, "node records must be tenant-scoped")
}

// TestTaxonomyRepository_TenantIsolation proves every tenant-scoped read and mutation refuses
// to touch another tenant's run and nodes.
func TestTaxonomyRepository_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	db := taxonomyTestDB(t)
	repo := repository.NewTaxonomyRepository(db)

	scope := uniqueTaxonomyScope("tax-isolation-a")
	ids := seedTaxonomyGraph(ctx, t, db, scope)

	otherTenant := "tax-isolation-b-" + uuid.NewString()

	t.Run("get run is tenant scoped but internal lookup is not", func(t *testing.T) {
		_, err := repo.GetRunForTenant(ctx, ids.RunID, scope.TenantID)
		require.NoError(t, err)

		_, err = repo.GetRunForTenant(ctx, ids.RunID, otherTenant)
		require.ErrorIs(t, err, huberrors.ErrNotFound)

		internalRun, err := repo.GetRunForInternalService(ctx, ids.RunID)
		require.NoError(t, err, "internal lookup intentionally has no tenant scope")
		require.Equal(t, scope.TenantID, internalRun.TenantID)
	})

	t.Run("get tree refuses another tenant", func(t *testing.T) {
		_, err := repo.GetTree(ctx, ids.RunID, otherTenant)
		require.ErrorIs(t, err, huberrors.ErrNotFound)
	})

	t.Run("rename and remove refuse another tenant", func(t *testing.T) {
		_, err := repo.RenameNode(ctx, ids.BranchID, otherTenant, "attacker", "Hijacked")
		require.ErrorIs(t, err, huberrors.ErrNotFound)

		_, err = repo.RemoveNode(ctx, ids.BranchID, otherTenant, "attacker")
		require.ErrorIs(t, err, huberrors.ErrNotFound)

		// The node is untouched by the rejected cross-tenant edits.
		tree, err := repo.GetTree(ctx, ids.RunID, scope.TenantID)
		require.NoError(t, err)
		require.True(t, treeContainsNode(tree.Root, ids.BranchID))
	})

	t.Run("list runs and active run are tenant scoped", func(t *testing.T) {
		runs, err := repo.ListRuns(ctx, models.ListTaxonomyRunsFilters{TenantID: otherTenant})
		require.NoError(t, err)
		require.Empty(t, runs)

		otherScope := scope
		otherScope.TenantID = otherTenant
		_, err = repo.GetActiveRun(ctx, otherScope)
		require.ErrorIs(t, err, huberrors.ErrNotFound)
	})
}

// TestTaxonomyRepository_TenantDeletionCleansTaxonomy proves a tenant data purge removes all
// taxonomy artifacts for the tenant while leaving another tenant's taxonomy intact.
func TestTaxonomyRepository_TenantDeletionCleansTaxonomy(t *testing.T) {
	ctx := context.Background()
	db := taxonomyTestDB(t)

	scopeA := uniqueTaxonomyScope("tax-delete-a")
	scopeB := uniqueTaxonomyScope("tax-delete-b")
	idsA := seedTaxonomyGraph(ctx, t, db, scopeA)
	idsB := seedTaxonomyGraph(ctx, t, db, scopeB)

	tenantDataRepo := repository.NewTenantDataRepository(db, time.Second)
	_, err := tenantDataRepo.DeleteByTenant(ctx, scopeA.TenantID)
	require.NoError(t, err)

	for _, table := range []string{
		"taxonomy_runs", "taxonomy_clusters", "taxonomy_cluster_memberships",
		"taxonomy_nodes", "taxonomy_active_runs", "taxonomy_node_events",
	} {
		var (
			count int64
			query string
		)

		switch table {
		case "taxonomy_runs":
			query = `SELECT COUNT(*) FROM taxonomy_runs WHERE id = $1`
		default:
			query = `SELECT COUNT(*) FROM ` + table + ` WHERE run_id = $1`
		}

		count = countTenantDataRows(ctx, t, db, query, idsA.RunID)
		assert.Equalf(t, int64(0), count, "%s must be cleared for the purged tenant", table)
	}

	// The other tenant's taxonomy run and artifacts survive the purge.
	assert.Equal(t, int64(1), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_runs WHERE id = $1`, idsB.RunID))
	assert.Equal(t, int64(3), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_nodes WHERE run_id = $1`, idsB.RunID))
}
