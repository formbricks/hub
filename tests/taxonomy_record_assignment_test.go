package tests

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/models"
)

func TestFeedbackRecordTaxonomyAPI(t *testing.T) {
	ctx := context.Background()
	harness := setupTaxonomyAPIServer(t)
	tenantID := uniqueTaxonomyScope("record-taxonomy").TenantID
	cleanupTaxonomyTenant(ctx, t, harness.db, tenantID)

	insertRecord := func() uuid.UUID {
		t.Helper()
		var id uuid.UUID
		err := harness.db.QueryRow(ctx, `
			INSERT INTO feedback_records (source_type, field_id, field_type, value_text, tenant_id, submission_id)
			VALUES ('survey', 'q1', 'text'::field_type_enum, 'Login was confusing', $1, $2)
			RETURNING id`, tenantID, uuid.NewString()).Scan(&id)
		require.NoError(t, err)
		return id
	}

	recordID := insertRecord()
	assignmentURL := func(id uuid.UUID, tenant string) string {
		return taxonomyURL(harness.server.URL, "/v1/feedback-records/"+id.String()+"/taxonomy",
			url.Values{"tenant_id": {tenant}})
	}
	get := func(id uuid.UUID) models.FeedbackRecordTaxonomyResponse {
		t.Helper()
		var result models.FeedbackRecordTaxonomyResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet, assignmentURL(id, tenantID), harness.apiKey,
			nil, http.StatusOK, &result)
		return result
	}

	t.Run("no active taxonomy is distinct from unclassified", func(t *testing.T) {
		result := get(recordID)
		assert.Equal(t, models.FeedbackRecordTaxonomyNoActiveTaxonomy, result.Status)
		assert.Nil(t, result.RunID)
		assert.Empty(t, result.Path)
	})

	t.Run("record and tenant authorization", func(t *testing.T) {
		requestTaxonomyProblem(ctx, t, http.MethodGet, assignmentURL(recordID, "other-tenant"),
			harness.apiKey, nil, http.StatusNotFound, response.CodeNotFound, response.ProblemTypeNotFound)
		requestTaxonomyProblem(ctx, t, http.MethodGet, assignmentURL(uuid.New(), tenantID),
			harness.apiKey, nil, http.StatusNotFound, response.CodeNotFound, response.ProblemTypeNotFound)
		requestTaxonomyProblem(ctx, t, http.MethodGet, assignmentURL(recordID, tenantID),
			"", nil, http.StatusUnauthorized, response.CodeUnauthorized, response.ProblemTypeUnauthorized)
		requestTaxonomyProblem(ctx, t, http.MethodGet,
			harness.server.URL+"/v1/feedback-records/not-a-uuid/taxonomy?tenant_id="+tenantID,
			harness.apiKey, nil, http.StatusBadRequest, response.CodeBadRequest, response.ProblemTypeBadRequest)
		requestTaxonomyProblem(ctx, t, http.MethodGet,
			harness.server.URL+"/v1/feedback-records/"+recordID.String()+"/taxonomy",
			harness.apiKey, nil, http.StatusBadRequest, response.CodeBadRequest, response.ProblemTypeBadRequest)
	})

	var runID, clusterID, rootID, topicID, subtopicID, detailID, leafID uuid.UUID
	err := harness.db.QueryRow(ctx, `
		INSERT INTO taxonomy_runs (tenant_id, scope_type, source_type, source_id, field_id, status)
		VALUES ($1, 'directory', '', '', '', 'succeeded') RETURNING id`, tenantID).Scan(&runID)
	require.NoError(t, err)
	err = harness.db.QueryRow(ctx, `
		INSERT INTO taxonomy_clusters (run_id, cluster_key, size) VALUES ($1, 1, 1) RETURNING id`,
		runID).Scan(&clusterID)
	require.NoError(t, err)
	_, err = harness.db.Exec(ctx, `
		INSERT INTO taxonomy_cluster_memberships (run_id, tenant_id, cluster_id, feedback_record_id)
		VALUES ($1, $2, $3, $4)`, runID, tenantID, clusterID, recordID)
	require.NoError(t, err)
	err = harness.db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (run_id, node_type, label, level)
		VALUES ($1, 'root', 'Feedback', 0) RETURNING id`, runID).Scan(&rootID)
	require.NoError(t, err)
	for _, node := range []struct {
		label  string
		level  int
		parent uuid.UUID
		id     *uuid.UUID
	}{
		{"Product", 1, rootID, &topicID},
		{"Authentication", 2, uuid.Nil, &subtopicID},
		{"Login", 3, uuid.Nil, &detailID},
	} {
		parent := node.parent
		if node.level == 2 {
			parent = topicID
		} else if node.level == 3 {
			parent = subtopicID
		}
		err = harness.db.QueryRow(ctx, `
			INSERT INTO taxonomy_nodes (run_id, parent_id, node_type, label, level)
			VALUES ($1, $2, 'branch', $3, $4) RETURNING id`,
			runID, parent, node.label, node.level).Scan(node.id)
		require.NoError(t, err)
	}
	err = harness.db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (run_id, parent_id, cluster_id, node_type, label, level)
		VALUES ($1, $2, $3, 'leaf', 'Password reset', 4) RETURNING id`,
		runID, detailID, clusterID).Scan(&leafID)
	require.NoError(t, err)
	_, err = harness.db.Exec(ctx, `
		INSERT INTO taxonomy_active_runs (tenant_id, scope_type, source_type, source_id, field_id, run_id)
		VALUES ($1, 'directory', '', '', '', $2)`, tenantID, runID)
	require.NoError(t, err)

	t.Run("classified path uses current visible labels in order", func(t *testing.T) {
		result := get(recordID)
		assert.Equal(t, models.FeedbackRecordTaxonomyClassified, result.Status)
		require.NotNil(t, result.RunID)
		assert.Equal(t, runID, *result.RunID)
		require.Len(t, result.Path, 4)
		assert.Equal(t, []uuid.UUID{topicID, subtopicID, detailID, leafID}, []uuid.UUID{
			result.Path[0].ID, result.Path[1].ID, result.Path[2].ID, result.Path[3].ID,
		})
		assert.Equal(t, []string{"Product", "Authentication", "Login", "Password reset"}, []string{
			result.Path[0].Label, result.Path[1].Label, result.Path[2].Label, result.Path[3].Label,
		})
	})

	t.Run("records outside the active run are unclassified", func(t *testing.T) {
		result := get(insertRecord())
		assert.Equal(t, models.FeedbackRecordTaxonomyUnclassified, result.Status)
		require.NotNil(t, result.RunID)
		assert.Equal(t, runID, *result.RunID)
		assert.Empty(t, result.Path)
	})

	t.Run("outlier leaf is a visible classification", func(t *testing.T) {
		outlierRecordID := insertRecord()
		var outlierClusterID uuid.UUID
		err := harness.db.QueryRow(ctx, `
			INSERT INTO taxonomy_clusters (run_id, cluster_key, size, is_outlier)
			VALUES ($1, -1, 1, true) RETURNING id`, runID).Scan(&outlierClusterID)
		require.NoError(t, err)
		_, err = harness.db.Exec(ctx, `
			INSERT INTO taxonomy_cluster_memberships (run_id, tenant_id, cluster_id, feedback_record_id)
			VALUES ($1, $2, $3, $4)`, runID, tenantID, outlierClusterID, outlierRecordID)
		require.NoError(t, err)
		_, err = harness.db.Exec(ctx, `
			INSERT INTO taxonomy_nodes (run_id, parent_id, cluster_id, node_type, label, level)
			VALUES ($1, $2, $3, 'leaf', 'Uncategorized Feedback', 4)`, runID, detailID, outlierClusterID)
		require.NoError(t, err)
		result := get(outlierRecordID)
		assert.Equal(t, models.FeedbackRecordTaxonomyClassified, result.Status)
		assert.Equal(t, "Uncategorized Feedback", result.Path[3].Label)
	})

	t.Run("rename reflects current label", func(t *testing.T) {
		_, err := harness.db.Exec(ctx, `UPDATE taxonomy_nodes SET label = 'Account access' WHERE id = $1`, subtopicID)
		require.NoError(t, err)
		result := get(recordID)
		assert.Equal(t, "Account access", result.Path[1].Label)
	})

	t.Run("removed ancestor hides the entire path", func(t *testing.T) {
		_, err := harness.db.Exec(ctx, `UPDATE taxonomy_nodes SET removed_at = NOW() WHERE id = $1`, topicID)
		require.NoError(t, err)
		result := get(recordID)
		assert.Equal(t, models.FeedbackRecordTaxonomyUnclassified, result.Status)
		assert.Empty(t, result.Path)
	})

	t.Run("new active run replaces historical membership", func(t *testing.T) {
		var nextRunID uuid.UUID
		err := harness.db.QueryRow(ctx, `
			INSERT INTO taxonomy_runs (tenant_id, scope_type, source_type, source_id, field_id, status)
			VALUES ($1, 'directory', '', '', '', 'succeeded') RETURNING id`, tenantID).Scan(&nextRunID)
		require.NoError(t, err)
		_, err = harness.db.Exec(ctx, `UPDATE taxonomy_active_runs SET run_id = $1 WHERE tenant_id = $2 AND scope_type = 'directory'`,
			nextRunID, tenantID)
		require.NoError(t, err)
		result := get(recordID)
		assert.Equal(t, models.FeedbackRecordTaxonomyUnclassified, result.Status)
		require.NotNil(t, result.RunID)
		assert.Equal(t, nextRunID, *result.RunID)
		assert.Empty(t, result.Path)
	})
}
