package tests

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/pkg/database"
)

func setupTaxonomySchemaTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultTestDatabaseURL
	}

	t.Setenv("DATABASE_URL", databaseURL)

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(context.Background(), cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
	)
	require.NoError(t, err)

	t.Cleanup(db.Close)

	return db
}

func TestTaxonomySchemaRelationshipsAndCascades(t *testing.T) {
	ctx := context.Background()
	db := setupTaxonomySchemaTestDB(t)

	tenantID := "taxonomy-schema-" + uuid.NewString()
	sourceType := "formbricks"
	sourceID := "feedback-directory-" + uuid.NewString()
	fieldID := "feedback"

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM taxonomy_runs WHERE tenant_id = $1`, tenantID)
		_, _ = db.Exec(ctx, `DELETE FROM feedback_records WHERE tenant_id = $1`, tenantID)
	})

	var feedbackRecordID uuid.UUID

	err := db.QueryRow(ctx, `
		INSERT INTO feedback_records (
			source_type, source_id, source_name, field_id, field_label, field_type,
			value_text, tenant_id, submission_id
		)
		VALUES ($1, $2, $3, $4, $5, $6::field_type_enum, $7, $8, $9)
		RETURNING id`,
		sourceType, sourceID, "Feedback Directory", fieldID, "Feedback", "text",
		"Login was confusing", tenantID, "submission-"+uuid.NewString(),
	).Scan(&feedbackRecordID)
	require.NoError(t, err)

	var runID uuid.UUID

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_runs (
			tenant_id, source_type, source_id, field_id, field_label, status,
			params, record_count, embedding_count
		)
		VALUES ($1, $2, $3, $4, $5, $6::taxonomy_run_status_enum, $7::jsonb, 1, 1)
		RETURNING id`,
		tenantID, sourceType, sourceID, fieldID, "Feedback", "running", `{"min_topic_size":10}`,
	).Scan(&runID)
	require.NoError(t, err)

	var clusterID uuid.UUID

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_clusters (run_id, cluster_key, label, llm_label, keywords, size)
		VALUES ($1, 1, $2, $3, $4::jsonb, 1)
		RETURNING id`,
		runID, "login,password", "Authentication issues", `["login","password"]`,
	).Scan(&clusterID)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_cluster_memberships (
			run_id, cluster_id, feedback_record_id, confidence, metadata
		)
		VALUES ($1, $2, $3, 0.91, $4::jsonb)`,
		runID, clusterID, feedbackRecordID, `{"rank":1}`,
	)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_cluster_memberships (
			run_id, cluster_id, feedback_record_id, confidence
		)
		VALUES ($1, $2, $3, 0.90)`,
		runID, clusterID, feedbackRecordID,
	)
	require.Error(t, err, "a feedback record can be assigned only once per run")

	var rootID uuid.UUID

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (run_id, node_type, label, original_label, level, sort_order)
		VALUES ($1, $2::taxonomy_node_type_enum, 'Feedback', 'Feedback', 0, 0)
		RETURNING id`,
		runID, "root",
	).Scan(&rootID)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_nodes (run_id, node_type, label, level, sort_order)
		VALUES ($1, $2::taxonomy_node_type_enum, 'Duplicate root', 0, 1)`,
		runID, "root",
	)
	require.Error(t, err, "a run can have only one root node")

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_nodes (run_id, node_type, label, level, sort_order)
		VALUES ($1, $2::taxonomy_node_type_enum, 'Orphan branch', 1, 0)`,
		runID, "branch",
	)
	require.Error(t, err, "non-root taxonomy nodes must have a parent")

	var otherRunID uuid.UUID

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_runs (
			tenant_id, source_type, source_id, field_id, field_label, status
		)
		VALUES ($1, $2, $3, $4, $5, $6::taxonomy_run_status_enum)
		RETURNING id`,
		tenantID, sourceType, sourceID, fieldID, "Feedback", "pending",
	).Scan(&otherRunID)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_nodes (run_id, parent_id, node_type, label, level, sort_order)
		VALUES ($1, $2, $3::taxonomy_node_type_enum, 'Cross-run child', 1, 0)`,
		otherRunID, rootID, "branch",
	)
	require.Error(t, err, "taxonomy node parents must belong to the same run")

	var branchID uuid.UUID

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (
			run_id, parent_id, node_type, label, original_label, level, sort_order
		)
		VALUES ($1, $2, $3::taxonomy_node_type_enum, 'Product Access', 'Product Access', 1, 0)
		RETURNING id`,
		runID, rootID, "branch",
	).Scan(&branchID)
	require.NoError(t, err)

	var leafID uuid.UUID

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (
			run_id, parent_id, cluster_id, node_type, label, original_label, level, sort_order
		)
		VALUES ($1, $2, $3, $4::taxonomy_node_type_enum, 'Login Problems', 'Login Problems', 2, 0)
		RETURNING id`,
		runID, branchID, clusterID, "leaf",
	).Scan(&leafID)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_active_runs (
			tenant_id, source_type, source_id, field_id, run_id, activated_by
		)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		tenantID, sourceType, sourceID, fieldID, runID, "user-1",
	)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_active_runs (
			tenant_id, source_type, source_id, field_id, run_id, activated_by
		)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		tenantID, sourceType, sourceID, fieldID, runID, "user-1",
	)
	require.Error(t, err, "only one active run is allowed per directory field")

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_node_events (
			tenant_id, source_type, source_id, field_id, run_id, node_id,
			event_type, actor_id, old_value, new_value
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			$7::taxonomy_node_event_type_enum, $8, $9::jsonb, $10::jsonb
		)`,
		tenantID, sourceType, sourceID, fieldID, runID, leafID,
		"rename", "user-1", `{"label":"Login Problems"}`, `{"label":"Authentication Problems"}`,
	)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `DELETE FROM feedback_records WHERE id = $1`, feedbackRecordID)
	require.NoError(t, err)
	require.Equal(t, int64(0), countTaxonomyRows(t, db, `
		SELECT COUNT(*) FROM taxonomy_cluster_memberships WHERE feedback_record_id = $1`,
		feedbackRecordID,
	))

	_, err = db.Exec(ctx, `DELETE FROM taxonomy_runs WHERE id = $1`, runID)
	require.NoError(t, err)

	require.Equal(t, int64(0), countTaxonomyRows(t, db, `SELECT COUNT(*) FROM taxonomy_clusters WHERE run_id = $1`, runID))
	require.Equal(t, int64(0), countTaxonomyRows(t, db, `SELECT COUNT(*) FROM taxonomy_nodes WHERE run_id = $1`, runID))
	require.Equal(t, int64(0), countTaxonomyRows(t, db, `SELECT COUNT(*) FROM taxonomy_active_runs WHERE run_id = $1`, runID))
	require.Equal(t, int64(0), countTaxonomyRows(t, db, `SELECT COUNT(*) FROM taxonomy_node_events WHERE run_id = $1`, runID))
}

func countTaxonomyRows(t *testing.T, db *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()

	var count int64

	err := db.QueryRow(context.Background(), query, args...).Scan(&count)
	require.NoError(t, err)

	return count
}
