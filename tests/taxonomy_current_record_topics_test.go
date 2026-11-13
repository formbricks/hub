package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
)

// recordTopicRow is one row of the taxonomy_current_record_topics view.
type recordTopicRow struct {
	submissionID  string
	runID         uuid.UUID
	topicID       uuid.UUID
	topicLabel    string
	subtopicID    uuid.UUID
	subtopicLabel string
	leafID        uuid.UUID
	isOutlier     bool
}

// directoryTree is a five-level directory taxonomy: root, topic (1), subtopic (2), detail (3) and a
// leaf (4) that holds one cluster.
type directoryTree struct {
	runID, rootID, topicID, subtopicID, detailID, leafID, clusterID uuid.UUID
}

// TestTaxonomyCurrentRecordTopicsView checks the view Cube reads against GetFeedbackRecordTaxonomy,
// the API's own resolution of a record's assignment: after every change to the taxonomy, a record
// has a view row exactly when the API calls it classified, and the row carries the API path's
// level-1 and level-2 nodes and its leaf.
func TestTaxonomyCurrentRecordTopicsView(t *testing.T) {
	ctx := context.Background()
	db := taxonomyTestDB(t)
	repo := repository.NewTaxonomyRepository(db)

	tenantID := uniqueDirectoryTaxonomyScope("record-topics-view").TenantID
	otherTenantID := uniqueDirectoryTaxonomyScope("record-topics-view-other").TenantID

	cleanupTaxonomyTenant(ctx, t, db, tenantID)
	cleanupTaxonomyTenant(ctx, t, db, otherTenantID)

	insertRecord := func(t *testing.T, tenant, submissionID string) uuid.UUID {
		t.Helper()

		var id uuid.UUID

		err := db.QueryRow(ctx, `
			INSERT INTO feedback_records (source_type, field_id, field_type, value_text, tenant_id, submission_id)
			VALUES ('survey', $1, 'text'::field_type_enum, 'Login was confusing', $2, $3)
			RETURNING id`, "q-"+uuid.NewString(), tenant, submissionID).Scan(&id)
		require.NoError(t, err)

		return id
	}

	viewRows := func(t *testing.T, tenant string) map[uuid.UUID]recordTopicRow {
		t.Helper()

		rows, err := db.Query(ctx, `
			SELECT feedback_record_id, submission_id, run_id, topic_node_id, topic_label,
			       subtopic_node_id, subtopic_label, leaf_node_id, is_outlier
			FROM taxonomy_current_record_topics
			WHERE tenant_id = $1`, tenant)
		require.NoError(t, err)

		defer rows.Close()

		result := map[uuid.UUID]recordTopicRow{}

		for rows.Next() {
			var (
				recordID uuid.UUID
				row      recordTopicRow
			)

			require.NoError(t, rows.Scan(&recordID, &row.submissionID, &row.runID, &row.topicID, &row.topicLabel,
				&row.subtopicID, &row.subtopicLabel, &row.leafID, &row.isOutlier))

			_, duplicate := result[recordID]
			require.False(t, duplicate, "record %s has more than one row", recordID)

			result[recordID] = row
		}

		require.NoError(t, rows.Err())

		return result
	}

	// assertParity checks every given record of a tenant against GetFeedbackRecordTaxonomy and
	// returns the view's rows for that tenant.
	assertParity := func(t *testing.T, tenant string, recordIDs ...uuid.UUID) map[uuid.UUID]recordTopicRow {
		t.Helper()

		rows := viewRows(t, tenant)

		for _, recordID := range recordIDs {
			assignment, err := repo.GetFeedbackRecordTaxonomy(ctx, recordID, tenant)
			require.NoError(t, err)

			row, hasRow := rows[recordID]
			if assignment.Status != models.FeedbackRecordTaxonomyClassified {
				assert.False(t, hasRow, "record %s is %s but has a view row", recordID, assignment.Status)

				continue
			}

			require.True(t, hasRow, "record %s is classified but has no view row", recordID)
			require.Len(t, assignment.Path, 4)
			require.NotNil(t, assignment.RunID)
			assert.Equal(t, *assignment.RunID, row.runID)
			assert.Equal(t, assignment.Path[0].ID, row.topicID)
			assert.Equal(t, assignment.Path[0].Label, row.topicLabel)
			assert.Equal(t, assignment.Path[1].ID, row.subtopicID)
			assert.Equal(t, assignment.Path[1].Label, row.subtopicLabel)
			assert.Equal(t, assignment.Path[3].ID, row.leafID)
		}

		return rows
	}

	submissionID := "submission-" + uuid.NewString()
	recordID := insertRecord(t, tenantID, submissionID)
	sameResponseRecordID := insertRecord(t, tenantID, submissionID)
	unclassifiedRecordID := insertRecord(t, tenantID, submissionID)
	outlierRecordID := insertRecord(t, tenantID, "submission-"+uuid.NewString())
	allRecords := []uuid.UUID{recordID, sameResponseRecordID, unclassifiedRecordID, outlierRecordID}

	tree := seedDirectoryTree(ctx, t, db, tenantID, "directory", "Product", "Authentication")
	addTaxonomyMember(ctx, t, db, tree.runID, tenantID, tree.clusterID, recordID)

	// A second leaf, under another topic, for the second answer in the same response.
	billing := addTopicBranch(ctx, t, db, tree.runID, tree.rootID, "Billing", "Invoices")
	addTaxonomyMember(ctx, t, db, tree.runID, tenantID, billing.clusterID, sameResponseRecordID)

	// The outlier cluster's leaf, under the first topic.
	var outlierClusterID uuid.UUID

	err := db.QueryRow(ctx, `
		INSERT INTO taxonomy_clusters (run_id, cluster_key, size, is_outlier, label)
		VALUES ($1, -1, 1, true, 'Uncategorized Feedback') RETURNING id`, tree.runID).Scan(&outlierClusterID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_nodes (run_id, parent_id, cluster_id, node_type, label, level)
		VALUES ($1, $2, $3, 'leaf', 'Uncategorized Feedback', 4)`, tree.runID, tree.detailID, outlierClusterID)
	require.NoError(t, err)
	addTaxonomyMember(ctx, t, db, tree.runID, tenantID, outlierClusterID, outlierRecordID)

	t.Run("a run that is not active has no rows", func(t *testing.T) {
		assert.Empty(t, assertParity(t, tenantID, allRecords...))
	})

	activateDirectoryRun(ctx, t, db, tenantID, tree.runID)

	t.Run("classified records carry their topic path", func(t *testing.T) {
		rows := assertParity(t, tenantID, allRecords...)
		require.Contains(t, rows, recordID)

		row := rows[recordID]
		assert.Equal(t, submissionID, row.submissionID)
		assert.Equal(t, tree.runID, row.runID)
		assert.Equal(t, tree.topicID, row.topicID)
		assert.Equal(t, "Product", row.topicLabel)
		assert.Equal(t, tree.subtopicID, row.subtopicID)
		assert.Equal(t, "Authentication", row.subtopicLabel)
		assert.Equal(t, tree.leafID, row.leafID)
		assert.False(t, row.isOutlier)
	})

	t.Run("answers of one response keep their own topics", func(t *testing.T) {
		rows := viewRows(t, tenantID)
		require.Contains(t, rows, sameResponseRecordID)
		assert.Equal(t, submissionID, rows[sameResponseRecordID].submissionID)
		assert.Equal(t, "Billing", rows[sameResponseRecordID].topicLabel)
		assert.Equal(t, "Invoices", rows[sameResponseRecordID].subtopicLabel)
		assert.NotContains(t, rows, unclassifiedRecordID, "a record outside the run has no row")
	})

	t.Run("the outlier leaf has a row flagged as an outlier", func(t *testing.T) {
		rows := viewRows(t, tenantID)
		require.Contains(t, rows, outlierRecordID)
		assert.True(t, rows[outlierRecordID].isOutlier)
		assert.Equal(t, "Product", rows[outlierRecordID].topicLabel)
	})

	t.Run("a rename shows on the next read", func(t *testing.T) {
		_, err := db.Exec(ctx, `UPDATE taxonomy_nodes SET label = 'Account access' WHERE id = $1`, tree.subtopicID)
		require.NoError(t, err)

		rows := assertParity(t, tenantID, allRecords...)
		assert.Equal(t, "Account access", rows[recordID].subtopicLabel)
	})

	t.Run("a removed node anywhere on the path hides the record", func(t *testing.T) {
		for _, node := range []struct {
			name string
			id   uuid.UUID
		}{
			{"leaf", tree.leafID},
			{"level 3", tree.detailID},
			{"subtopic", tree.subtopicID},
			{"topic", tree.topicID},
			{"root", tree.rootID},
		} {
			_, err := db.Exec(ctx, `UPDATE taxonomy_nodes SET removed_at = NOW() WHERE id = $1`, node.id)
			require.NoError(t, err)

			rows := assertParity(t, tenantID, allRecords...)
			assert.NotContains(t, rows, recordID, "removed %s", node.name)

			_, err = db.Exec(ctx, `UPDATE taxonomy_nodes SET removed_at = NULL WHERE id = $1`, node.id)
			require.NoError(t, err)
		}

		assert.Contains(t, assertParity(t, tenantID, allRecords...), recordID)
	})

	t.Run("field-scope runs are ignored", func(t *testing.T) {
		fieldRecordID := insertRecord(t, tenantID, "submission-"+uuid.NewString())
		fieldTree := seedDirectoryTree(ctx, t, db, tenantID, "field", "Field topic", "Field subtopic")
		addTaxonomyMember(ctx, t, db, fieldTree.runID, tenantID, fieldTree.clusterID, fieldRecordID)
		_, err := db.Exec(ctx, `
			INSERT INTO taxonomy_active_runs (tenant_id, scope_type, source_type, source_id, field_id, run_id)
			VALUES ($1, 'field', 'survey', 'survey-1', 'q1', $2)`, tenantID, fieldTree.runID)
		require.NoError(t, err)

		assert.NotContains(t, assertParity(t, tenantID, fieldRecordID), fieldRecordID)
	})

	t.Run("tenants stay apart, even with the same submission id", func(t *testing.T) {
		otherRecordID := insertRecord(t, otherTenantID, submissionID)
		otherTree := seedDirectoryTree(ctx, t, db, otherTenantID, "directory", "Shipping", "Late delivery")
		addTaxonomyMember(ctx, t, db, otherTree.runID, otherTenantID, otherTree.clusterID, otherRecordID)
		activateDirectoryRun(ctx, t, db, otherTenantID, otherTree.runID)

		rows := assertParity(t, tenantID, allRecords...)
		otherRows := assertParity(t, otherTenantID, otherRecordID)

		assert.NotContains(t, rows, otherRecordID)
		require.Len(t, otherRows, 1)
		assert.Equal(t, "Shipping", otherRows[otherRecordID].topicLabel)

		for id := range rows {
			assert.NotContains(t, otherRows, id)
		}
	})

	t.Run("a newly activated run replaces the old assignment", func(t *testing.T) {
		nextTree := seedDirectoryTree(ctx, t, db, tenantID, "directory", "Onboarding", "Sign-up")
		addTaxonomyMember(ctx, t, db, nextTree.runID, tenantID, nextTree.clusterID, recordID)
		_, err := db.Exec(ctx, `
			UPDATE taxonomy_active_runs SET run_id = $1
			WHERE tenant_id = $2 AND scope_type = 'directory'`, nextTree.runID, tenantID)
		require.NoError(t, err)

		rows := assertParity(t, tenantID, allRecords...)
		require.Len(t, rows, 1, "only the record in the new run is classified")
		assert.Equal(t, nextTree.runID, rows[recordID].runID)
		assert.Equal(t, "Onboarding", rows[recordID].topicLabel)
		assert.Equal(t, "Sign-up", rows[recordID].subtopicLabel)

		_, err = db.Exec(ctx, `DELETE FROM taxonomy_active_runs WHERE tenant_id = $1 AND scope_type = 'directory'`, tenantID)
		require.NoError(t, err)
		assert.Empty(t, assertParity(t, tenantID, allRecords...), "no active run, no rows")
	})
}

// seedDirectoryTree inserts a succeeded run with a five-level tree, a topic and a subtopic as given,
// and one cluster in its leaf. scopeType "field" gives the run a field scope instead.
func seedDirectoryTree(
	ctx context.Context, t *testing.T, db *pgxpool.Pool, tenantID, scopeType, topic, subtopic string,
) directoryTree {
	t.Helper()

	var tree directoryTree

	sourceType, sourceID, fieldID := "", "", ""
	if scopeType == "field" {
		sourceType, sourceID, fieldID = "survey", "survey-1", "q1"
	}

	err := db.QueryRow(ctx, `
		INSERT INTO taxonomy_runs (tenant_id, scope_type, source_type, source_id, field_id, status)
		VALUES ($1, $2, $3, $4, $5, 'succeeded') RETURNING id`,
		tenantID, scopeType, sourceType, sourceID, fieldID).Scan(&tree.runID)
	require.NoError(t, err)
	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (run_id, node_type, label, level)
		VALUES ($1, 'root', 'Feedback', 0) RETURNING id`, tree.runID).Scan(&tree.rootID)
	require.NoError(t, err)

	branch := addTopicBranch(ctx, t, db, tree.runID, tree.rootID, topic, subtopic)
	tree.topicID, tree.subtopicID, tree.detailID = branch.topicID, branch.subtopicID, branch.detailID
	tree.leafID, tree.clusterID = branch.leafID, branch.clusterID

	return tree
}

// addTopicBranch adds a topic, subtopic, level-3 node and leaf under root, with a new cluster in the
// leaf. The cluster key is unique within the run.
func addTopicBranch(
	ctx context.Context, t *testing.T, db *pgxpool.Pool, runID, rootID uuid.UUID, topic, subtopic string,
) directoryTree {
	t.Helper()

	branch := directoryTree{runID: runID, rootID: rootID}

	err := db.QueryRow(ctx, `
		INSERT INTO taxonomy_clusters (run_id, cluster_key, size)
		VALUES ($1, (SELECT COALESCE(MAX(cluster_key), -1) + 1 FROM taxonomy_clusters WHERE run_id = $1), 1)
		RETURNING id`, runID).Scan(&branch.clusterID)
	require.NoError(t, err)

	parent := rootID

	for _, node := range []struct {
		label string
		level int
		id    *uuid.UUID
	}{
		{topic, 1, &branch.topicID},
		{subtopic, 2, &branch.subtopicID},
		{subtopic + " detail", 3, &branch.detailID},
	} {
		err = db.QueryRow(ctx, `
			INSERT INTO taxonomy_nodes (run_id, parent_id, node_type, label, level)
			VALUES ($1, $2, 'branch', $3, $4) RETURNING id`,
			runID, parent, node.label, node.level).Scan(node.id)
		require.NoError(t, err)

		parent = *node.id
	}

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (run_id, parent_id, cluster_id, node_type, label, level)
		VALUES ($1, $2, $3, 'leaf', $4, 4) RETURNING id`,
		runID, branch.detailID, branch.clusterID, subtopic+" leaf").Scan(&branch.leafID)
	require.NoError(t, err)

	return branch
}

func addTaxonomyMember(
	ctx context.Context, t *testing.T, db *pgxpool.Pool, runID uuid.UUID, tenantID string, clusterID, recordID uuid.UUID,
) {
	t.Helper()

	_, err := db.Exec(ctx, `
		INSERT INTO taxonomy_cluster_memberships (run_id, tenant_id, cluster_id, feedback_record_id)
		VALUES ($1, $2, $3, $4)`, runID, tenantID, clusterID, recordID)
	require.NoError(t, err)
}

func activateDirectoryRun(ctx context.Context, t *testing.T, db *pgxpool.Pool, tenantID string, runID uuid.UUID) {
	t.Helper()

	_, err := db.Exec(ctx, `
		INSERT INTO taxonomy_active_runs (tenant_id, scope_type, source_type, source_id, field_id, run_id)
		VALUES ($1, 'directory', '', '', '', $2)`, tenantID, runID)
	require.NoError(t, err)
}
