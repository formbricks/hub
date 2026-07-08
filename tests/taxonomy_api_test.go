package tests

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
)

// taxonomyURL joins the server base, a path, and optional query params.
func taxonomyURL(base, path string, query url.Values) string {
	u := base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	return u
}

// requestTaxonomyJSON issues a taxonomy HTTP request, asserts the status code, and (when
// out is non-nil) decodes the JSON body. It always closes the response body.
func requestTaxonomyJSON(
	ctx context.Context, t *testing.T, method, url, token string, body any, wantStatus int, out any,
) {
	t.Helper()

	resp := doTaxonomyRequest(ctx, t, method, url, token, body)

	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, wantStatus, resp.StatusCode)

	if out != nil {
		require.NoError(t, decodeData(resp, out))
	}
}

// findTaxonomyNode returns the first node in the tree matching predicate.
func findTaxonomyNode(node *models.TaxonomyNode, predicate func(models.TaxonomyNode) bool) *models.TaxonomyNode {
	if node == nil {
		return nil
	}

	if predicate(*node) {
		return node
	}

	for i := range node.Children {
		if found := findTaxonomyNode(&node.Children[i], predicate); found != nil {
			return found
		}
	}

	return nil
}

// TestTaxonomyAPI_Auth covers both auth layers: the public Hub API key and the internal
// service token, including that neither credential is accepted by the other layer.
func TestTaxonomyAPI_Auth(t *testing.T) {
	ctx := context.Background()
	harness := setupTaxonomyAPIServer(t)

	fieldsURL := taxonomyURL(harness.server.URL, "/v1/taxonomy/fields", url.Values{"tenant_id": {"auth-tenant"}})
	authCheckURL := harness.server.URL + "/internal/v1/taxonomy/auth-check"

	t.Run("public endpoint rejects missing and wrong credentials", func(t *testing.T) {
		requestTaxonomyJSON(ctx, t, http.MethodGet, fieldsURL, "", nil, http.StatusUnauthorized, nil)
		requestTaxonomyJSON(ctx, t, http.MethodGet, fieldsURL, "wrong-key", nil, http.StatusUnauthorized, nil)
	})

	t.Run("public endpoint accepts the Hub API key", func(t *testing.T) {
		requestTaxonomyJSON(ctx, t, http.MethodGet, fieldsURL, harness.apiKey, nil, http.StatusOK, nil)
	})

	t.Run("internal endpoint rejects missing and wrong credentials", func(t *testing.T) {
		requestTaxonomyJSON(ctx, t, http.MethodGet, authCheckURL, "", nil, http.StatusUnauthorized, nil)
		requestTaxonomyJSON(ctx, t, http.MethodGet, authCheckURL, "wrong-token", nil, http.StatusUnauthorized, nil)
	})

	t.Run("internal endpoint accepts the internal token", func(t *testing.T) {
		var body map[string]any
		requestTaxonomyJSON(ctx, t, http.MethodGet, authCheckURL, harness.internalToken, nil, http.StatusOK, &body)
		assert.Equal(t, "ok", body["status"])
	})

	t.Run("credentials do not cross auth layers", func(t *testing.T) {
		// The internal token must not authorize the public API...
		requestTaxonomyJSON(ctx, t, http.MethodGet, fieldsURL, harness.internalToken, nil, http.StatusUnauthorized, nil)
		// ...and the public API key must not authorize the internal service API.
		requestTaxonomyJSON(ctx, t, http.MethodGet, authCheckURL, harness.apiKey, nil, http.StatusUnauthorized, nil)
	})
}

// TestTaxonomyAPI_PublicReadAndEdit covers the public read and edit endpoints against a
// seeded, activated taxonomy graph.
func TestTaxonomyAPI_PublicReadAndEdit(t *testing.T) {
	ctx := context.Background()
	harness := setupTaxonomyAPIServer(t)

	scope := uniqueTaxonomyScope("tax-api-read")
	ids := seedTaxonomyGraph(ctx, t, harness.db, scope)

	scopeQuery := url.Values{
		"tenant_id":   {scope.TenantID},
		"source_type": {scope.SourceType},
		"source_id":   {scope.SourceID},
		"field_id":    {scope.FieldID},
	}
	tenantQuery := url.Values{"tenant_id": {scope.TenantID}}

	t.Run("list runs returns the seeded run", func(t *testing.T) {
		var resp models.ListTaxonomyRunsResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/runs", tenantQuery), harness.apiKey, nil, http.StatusOK, &resp)

		require.Len(t, resp.Data, 1)
		assert.Equal(t, ids.RunID, resp.Data[0].ID)
	})

	t.Run("get run returns the run", func(t *testing.T) {
		var run models.TaxonomyRun
		requestTaxonomyJSON(ctx, t, http.MethodGet,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/runs/"+ids.RunID.String(), tenantQuery), harness.apiKey, nil, http.StatusOK, &run)

		assert.Equal(t, ids.RunID, run.ID)
		assert.Equal(t, models.TaxonomyRunStatusSucceeded, run.Status)
	})

	t.Run("get run tree returns the hierarchy", func(t *testing.T) {
		var tree models.TaxonomyTreeResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/runs/"+ids.RunID.String()+"/tree", tenantQuery), harness.apiKey, nil, http.StatusOK, &tree)

		require.NotNil(t, tree.Root)
		assert.Equal(t, ids.RootID, tree.Root.ID)
		require.True(t, treeContainsNode(tree.Root, ids.LeafID))
	})

	t.Run("get active tree returns the active run tree", func(t *testing.T) {
		var tree models.TaxonomyTreeResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/runs/active/tree", scopeQuery), harness.apiKey, nil, http.StatusOK, &tree)

		assert.Equal(t, ids.RunID, tree.Run.ID)
		require.NotNil(t, tree.Root)
	})

	t.Run("list node records returns the assigned feedback", func(t *testing.T) {
		recordsURL := taxonomyURL(harness.server.URL, "/v1/taxonomy/nodes/"+ids.RootID.String()+"/records", tenantQuery)

		var resp models.TaxonomyNodeRecordsResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet, recordsURL, harness.apiKey, nil, http.StatusOK, &resp)

		require.Len(t, resp.Data, 1)
		assert.Equal(t, ids.FeedbackRecordID, resp.Data[0].ID)
	})

	t.Run("rename node updates the label", func(t *testing.T) {
		body := models.RenameTaxonomyNodeRequest{TenantID: scope.TenantID, ActorID: "api-actor", Label: "Renamed Access"}

		var node models.TaxonomyNode
		requestTaxonomyJSON(ctx, t, http.MethodPatch,
			harness.server.URL+"/v1/taxonomy/nodes/"+ids.BranchID.String(), harness.apiKey, body, http.StatusOK, &node)

		assert.Equal(t, "Renamed Access", node.Label)
	})

	t.Run("remove node soft-deletes it", func(t *testing.T) {
		removeQuery := url.Values{"tenant_id": {scope.TenantID}, "actor_id": {"api-actor"}}

		var node models.TaxonomyNode
		requestTaxonomyJSON(ctx, t, http.MethodDelete,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/nodes/"+ids.LeafID.String(), removeQuery), harness.apiKey, nil, http.StatusOK, &node)

		require.NotNil(t, node.RemovedAt)

		// The removed leaf no longer appears in the tree.
		var tree models.TaxonomyTreeResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/runs/"+ids.RunID.String()+"/tree", tenantQuery), harness.apiKey, nil, http.StatusOK, &tree)
		require.False(t, treeContainsNode(tree.Root, ids.LeafID))
	})
}

// TestTaxonomyAPI_TenantIsolation proves the public endpoints reject another tenant's
// identifiers: reads and edits 404, and node record drilldown returns nothing.
func TestTaxonomyAPI_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	harness := setupTaxonomyAPIServer(t)

	scope := uniqueTaxonomyScope("tax-api-iso")
	ids := seedTaxonomyGraph(ctx, t, harness.db, scope)

	otherTenant := "tax-api-iso-other-" + uuid.NewString()
	otherQuery := url.Values{"tenant_id": {otherTenant}}

	t.Run("get run 404s for another tenant", func(t *testing.T) {
		requestTaxonomyJSON(ctx, t, http.MethodGet,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/runs/"+ids.RunID.String(), otherQuery), harness.apiKey, nil, http.StatusNotFound, nil)
	})

	t.Run("get tree 404s for another tenant", func(t *testing.T) {
		treeURL := taxonomyURL(harness.server.URL, "/v1/taxonomy/runs/"+ids.RunID.String()+"/tree", otherQuery)
		requestTaxonomyJSON(ctx, t, http.MethodGet, treeURL, harness.apiKey, nil, http.StatusNotFound, nil)
	})

	t.Run("rename 404s for another tenant", func(t *testing.T) {
		body := models.RenameTaxonomyNodeRequest{TenantID: otherTenant, ActorID: "attacker", Label: "Hijacked"}
		requestTaxonomyJSON(ctx, t, http.MethodPatch,
			harness.server.URL+"/v1/taxonomy/nodes/"+ids.BranchID.String(), harness.apiKey, body, http.StatusNotFound, nil)
	})

	t.Run("remove 404s for another tenant", func(t *testing.T) {
		removeQuery := url.Values{"tenant_id": {otherTenant}, "actor_id": {"attacker"}}
		requestTaxonomyJSON(ctx, t, http.MethodDelete,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/nodes/"+ids.BranchID.String(), removeQuery), harness.apiKey, nil, http.StatusNotFound, nil)
	})

	t.Run("node records return nothing for another tenant", func(t *testing.T) {
		recordsURL := taxonomyURL(harness.server.URL, "/v1/taxonomy/nodes/"+ids.RootID.String()+"/records", otherQuery)

		var resp models.TaxonomyNodeRecordsResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet, recordsURL, harness.apiKey, nil, http.StatusOK, &resp)
		require.Empty(t, resp.Data)
	})
}

// TestTaxonomyAPI_RecordCounts covers GET /v1/taxonomy/runs/{run_id}/record-counts: subtree
// totals per node against real data, tenant isolation, and input validation.
func TestTaxonomyAPI_RecordCounts(t *testing.T) {
	ctx := context.Background()
	harness := setupTaxonomyAPIServer(t)

	scope := uniqueTaxonomyScope("tax-api-count")
	ids := seedTaxonomyGraph(ctx, t, harness.db, scope)

	// Add a second feedback record to the leaf's cluster so counts exceed the node count and
	// prove we are counting distinct records, not nodes or clusters.
	var secondRecordID uuid.UUID

	err := harness.db.QueryRow(ctx, `
		INSERT INTO feedback_records (
			source_type, source_id, field_id, field_label, field_type,
			value_text, tenant_id, submission_id
		)
		VALUES ($1, NULLIF($2, ''), $3, 'Feedback', 'text'::field_type_enum, $4, $5, $6)
		RETURNING id`,
		scope.SourceType, scope.SourceID, scope.FieldID,
		"Login still confusing", scope.TenantID, "submission-"+uuid.NewString(),
	).Scan(&secondRecordID)
	require.NoError(t, err)

	_, err = harness.db.Exec(ctx, `
		INSERT INTO taxonomy_cluster_memberships (run_id, tenant_id, cluster_id, feedback_record_id, confidence)
		VALUES ($1, $2, $3, $4, 0.90)`,
		ids.RunID, scope.TenantID, ids.ClusterID, secondRecordID,
	)
	require.NoError(t, err)

	tenantQuery := url.Values{"tenant_id": {scope.TenantID}}
	countsURL := func(runID string, q url.Values) string {
		return taxonomyURL(harness.server.URL, "/v1/taxonomy/runs/"+runID+"/record-counts", q)
	}

	t.Run("returns subtree totals per node", func(t *testing.T) {
		var resp models.TaxonomyRecordCountsResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet, countsURL(ids.RunID.String(), tenantQuery),
			harness.apiKey, nil, http.StatusOK, &resp)

		byNode := make(map[uuid.UUID]int64, len(resp.Counts))
		for _, c := range resp.Counts {
			byNode[c.NodeID] = c.RecordCount
		}

		// Seed graph is root -> branch -> leaf; the leaf's cluster now holds two records.
		// The branch and root carry no cluster of their own, so their counts can only come
		// from rolling up the leaf.
		assert.Equal(t, int64(2), byNode[ids.LeafID], "leaf holds its two records")
		assert.Equal(t, int64(2), byNode[ids.BranchID], "branch rolls up its leaf")
		assert.Equal(t, int64(2), byNode[ids.RootID], "root rolls up the whole run")
	})

	t.Run("404s for another tenant", func(t *testing.T) {
		otherQuery := url.Values{"tenant_id": {"tax-api-count-other-" + uuid.NewString()}}
		requestTaxonomyJSON(ctx, t, http.MethodGet, countsURL(ids.RunID.String(), otherQuery),
			harness.apiKey, nil, http.StatusNotFound, nil)
	})

	t.Run("404s for an unknown run", func(t *testing.T) {
		requestTaxonomyJSON(ctx, t, http.MethodGet, countsURL(uuid.NewString(), tenantQuery),
			harness.apiKey, nil, http.StatusNotFound, nil)
	})

	t.Run("400s for an invalid run ID", func(t *testing.T) {
		requestTaxonomyJSON(ctx, t, http.MethodGet, countsURL("not-a-uuid", tenantQuery),
			harness.apiKey, nil, http.StatusBadRequest, nil)
	})

	t.Run("400s when tenant_id is missing", func(t *testing.T) {
		requestTaxonomyJSON(ctx, t, http.MethodGet, countsURL(ids.RunID.String(), url.Values{}),
			harness.apiKey, nil, http.StatusBadRequest, nil)
	})
}

// TestTaxonomyAPI_CreateRun covers the public run-creation endpoint: validation failures,
// insufficient embedded input, the accepted happy path, and in-progress reuse.
func TestTaxonomyAPI_CreateRun(t *testing.T) {
	ctx := context.Background()
	harness := setupTaxonomyAPIServer(t)
	runsURL := harness.server.URL + "/v1/taxonomy/runs"

	t.Run("missing scope fields are rejected", func(t *testing.T) {
		requestTaxonomyJSON(ctx, t, http.MethodPost, runsURL, harness.apiKey,
			map[string]any{"source_type": "formbricks"}, http.StatusBadRequest, nil)
	})

	t.Run("insufficient embedded records are rejected", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-api-create-insufficient")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		// One text record but no embeddings: below the embedding threshold.
		insertScopeFeedbackRecord(ctx, t, harness.db, scope)

		body := models.CreateTaxonomyRunRequest{TaxonomyScope: scope}
		requestTaxonomyJSON(ctx, t, http.MethodPost, runsURL, harness.apiKey, body, http.StatusBadRequest, nil)
	})

	t.Run("directory scope rejects field selectors", func(t *testing.T) {
		scope := uniqueDirectoryTaxonomyScope("tax-api-create-directory-invalid")
		scope.SourceType = "formbricks"

		body := models.CreateTaxonomyRunRequest{TaxonomyScope: scope}
		requestTaxonomyJSON(ctx, t, http.MethodPost, runsURL, harness.apiKey, body, http.StatusBadRequest, nil)
	})

	t.Run("accepts a new run and reuses an in-progress one", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-api-create-ok")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		seedEmbeddedFeedback(ctx, t, harness, scope, taxonomyMinEmbeddedRecords+1)

		body := models.CreateTaxonomyRunRequest{TaxonomyScope: scope, ActorID: new("creator")}

		var first models.CreateTaxonomyRunResponse
		requestTaxonomyJSON(ctx, t, http.MethodPost, runsURL, harness.apiKey, body, http.StatusAccepted, &first)
		require.False(t, first.InProgress)
		assert.Equal(t, models.TaxonomyRunStatusRunning, first.Run.Status)
		assert.Contains(t, harness.starter.startedRunIDs(), first.Run.ID.String())

		// A second request for the same scope reuses the running run (200, in_progress).
		var second models.CreateTaxonomyRunResponse
		requestTaxonomyJSON(ctx, t, http.MethodPost, runsURL, harness.apiKey, body, http.StatusOK, &second)
		require.True(t, second.InProgress)
		assert.Equal(t, first.Run.ID, second.Run.ID)
	})

	t.Run("accepts a directory run across sources and fields", func(t *testing.T) {
		directoryScope := uniqueDirectoryTaxonomyScope("tax-api-create-directory")
		cleanupTaxonomyTenant(ctx, t, harness.db, directoryScope.TenantID)

		firstFieldScope := models.TaxonomyScope{
			TenantID:   directoryScope.TenantID,
			SourceType: "formbricks",
			SourceID:   "survey-" + uuid.NewString(),
			FieldID:    "ces_comment",
		}
		secondFieldScope := models.TaxonomyScope{
			TenantID:   directoryScope.TenantID,
			SourceType: "support",
			SourceID:   "ticket-" + uuid.NewString(),
			FieldID:    "support_comment",
		}

		seedEmbeddedFeedback(ctx, t, harness, firstFieldScope, taxonomyMinEmbeddedRecords)
		seedEmbeddedFeedback(ctx, t, harness, secondFieldScope, taxonomyMinEmbeddedRecords+1)

		body := models.CreateTaxonomyRunRequest{TaxonomyScope: directoryScope}

		var first models.CreateTaxonomyRunResponse
		requestTaxonomyJSON(ctx, t, http.MethodPost, runsURL, harness.apiKey, body, http.StatusAccepted, &first)
		require.False(t, first.InProgress)
		assert.Equal(t, models.TaxonomyScopeTypeDirectory, first.Run.ScopeType)
		assert.Empty(t, first.Run.SourceType)
		assert.Empty(t, first.Run.SourceID)
		assert.Empty(t, first.Run.FieldID)
		require.NotNil(t, first.Run.FieldLabel)
		assert.Equal(t, "All feedback", *first.Run.FieldLabel)
		assert.Equal(t, taxonomyMinEmbeddedRecords*2+1, first.Run.RecordCount)
		assert.Equal(t, taxonomyMinEmbeddedRecords*2+1, first.Run.EmbeddingCount)

		var second models.CreateTaxonomyRunResponse
		requestTaxonomyJSON(ctx, t, http.MethodPost, runsURL, harness.apiKey, body, http.StatusOK, &second)
		require.True(t, second.InProgress)
		assert.Equal(t, first.Run.ID, second.Run.ID)
	})
}

// TestTaxonomyAPI_InternalServiceEndpoints covers the internal service flow: fetching run
// input, completing a run (storing artifacts and activating), and failing a run.
func TestTaxonomyAPI_InternalServiceEndpoints(t *testing.T) {
	ctx := context.Background()
	harness := setupTaxonomyAPIServer(t)

	t.Run("get run input returns feedback text and embeddings", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-input")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		seedEmbeddedFeedback(ctx, t, harness, scope, taxonomyMinEmbeddedRecords+1)

		runID := startRunForScope(ctx, t, harness, scope)

		inputURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/input"

		// Auth is required.
		requestTaxonomyJSON(ctx, t, http.MethodGet, inputURL, "", nil, http.StatusUnauthorized, nil)

		var input models.TaxonomyRunInputResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet, inputURL, harness.internalToken, nil, http.StatusOK, &input)
		assert.Equal(t, runID, input.Run.ID)
		require.NotEmpty(t, input.Records)
		assert.NotEmpty(t, input.Records[0].Embedding)
		assert.NotEmpty(t, input.Records[0].ValueText)
	})

	t.Run("directory run input spans sources and fields", func(t *testing.T) {
		directoryScope := uniqueDirectoryTaxonomyScope("tax-internal-directory-input")
		cleanupTaxonomyTenant(ctx, t, harness.db, directoryScope.TenantID)

		firstFieldScope := models.TaxonomyScope{
			TenantID:   directoryScope.TenantID,
			SourceType: "formbricks",
			SourceID:   "survey-" + uuid.NewString(),
			FieldID:    "ces_comment",
		}
		secondFieldScope := models.TaxonomyScope{
			TenantID:   directoryScope.TenantID,
			SourceType: "support",
			SourceID:   "ticket-" + uuid.NewString(),
			FieldID:    "support_comment",
		}

		seedEmbeddedFeedback(ctx, t, harness, firstFieldScope, taxonomyMinEmbeddedRecords)
		seedEmbeddedFeedback(ctx, t, harness, secondFieldScope, taxonomyMinEmbeddedRecords+1)

		runID := startRunForScope(ctx, t, harness, directoryScope)
		inputURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/input"

		var input models.TaxonomyRunInputResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet, inputURL, harness.internalToken, nil, http.StatusOK, &input)
		assert.Equal(t, models.TaxonomyScopeTypeDirectory, input.Run.ScopeType)
		require.Len(t, input.Records, taxonomyMinEmbeddedRecords*2+1)

		fieldIDs := map[string]bool{}
		sourceTypes := map[string]bool{}

		for _, record := range input.Records {
			fieldIDs[record.FieldID] = true
			sourceTypes[record.SourceType] = true
			assert.NotEmpty(t, record.Embedding)
			assert.NotEmpty(t, record.ValueText)
		}

		assert.True(t, fieldIDs["ces_comment"])
		assert.True(t, fieldIDs["support_comment"])
		assert.True(t, sourceTypes["formbricks"])
		assert.True(t, sourceTypes["support"])
	})

	t.Run("complete run stores artifacts and activates", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-complete")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)

		feedbackRecordID := insertScopeFeedbackRecord(ctx, t, harness.db, scope)
		runID := createRunningRun(ctx, t, harness, scope)

		result := models.TaxonomyRunResultRequest{
			Clusters: []models.TaxonomyResultCluster{
				{ClusterKey: 1, Label: new("login"), Size: 1},
			},
			Memberships: []models.TaxonomyResultMembership{
				{ClusterKey: 1, FeedbackRecordID: feedbackRecordID, Confidence: new(0.9)},
			},
			Nodes: []models.TaxonomyResultNode{
				{NodeKey: "root", NodeType: models.TaxonomyNodeTypeRoot, Label: "Feedback", Level: 0},
				{NodeKey: "leaf", ParentKey: new("root"), ClusterKey: new(1), NodeType: models.TaxonomyNodeTypeLeaf, Label: "Login", Level: 1},
			},
		}

		resultURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/result"

		// Auth is required.
		requestTaxonomyJSON(ctx, t, http.MethodPut, resultURL, "", result, http.StatusUnauthorized, nil)

		var run models.TaxonomyRun
		requestTaxonomyJSON(ctx, t, http.MethodPut, resultURL, harness.internalToken, result, http.StatusOK, &run)
		assert.Equal(t, models.TaxonomyRunStatusSucceeded, run.Status)

		// The completed run is now the active run for its scope.
		active, err := harness.repo.GetActiveRun(ctx, scope)
		require.NoError(t, err)
		assert.Equal(t, runID, active.ID)
	})

	t.Run("fail run records the failure", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-fail")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)

		runID := createRunningRun(ctx, t, harness, scope)

		body := models.TaxonomyRunFailedRequest{
			Error:     "clustering did not converge",
			ErrorCode: models.TaxonomyRunFailureCodeGenerationFailed,
		}
		failedURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/failed"

		// Auth is required.
		requestTaxonomyJSON(ctx, t, http.MethodPost, failedURL, "", body, http.StatusUnauthorized, nil)

		var run models.TaxonomyRun
		requestTaxonomyJSON(ctx, t, http.MethodPost, failedURL, harness.internalToken, body, http.StatusOK, &run)
		assert.Equal(t, models.TaxonomyRunStatusFailed, run.Status)
		require.NotNil(t, run.Error)
		assert.Equal(t, "clustering did not converge", *run.Error)
	})
}

// TestTaxonomyAPI_GenerationLifecycle drives the full path across both API layers: a public
// create, the internal service fetching input and posting the result, then the public active
// tree reflecting the generated taxonomy.
func TestTaxonomyAPI_GenerationLifecycle(t *testing.T) {
	ctx := context.Background()
	harness := setupTaxonomyAPIServer(t)

	scope := uniqueTaxonomyScope("tax-lifecycle")
	cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
	seedEmbeddedFeedback(ctx, t, harness, scope, taxonomyMinEmbeddedRecords+1)

	// 1. Public create -> running run.
	var created models.CreateTaxonomyRunResponse
	requestTaxonomyJSON(ctx, t, http.MethodPost, harness.server.URL+"/v1/taxonomy/runs", harness.apiKey,
		models.CreateTaxonomyRunRequest{TaxonomyScope: scope}, http.StatusAccepted, &created)
	runID := created.Run.ID

	// 2. Internal service fetches the run input (feedback + embeddings).
	var input models.TaxonomyRunInputResponse
	requestTaxonomyJSON(ctx, t, http.MethodGet, harness.server.URL+"/internal/v1/taxonomy/runs/"+runID.String()+"/input",
		harness.internalToken, nil, http.StatusOK, &input)
	require.NotEmpty(t, input.Records)

	// 3. Internal service posts the generated result referencing a real input record.
	result := models.TaxonomyRunResultRequest{
		Clusters: []models.TaxonomyResultCluster{{ClusterKey: 1, Label: new("login"), Size: len(input.Records)}},
		Memberships: []models.TaxonomyResultMembership{
			{ClusterKey: 1, FeedbackRecordID: input.Records[0].FeedbackRecordID, Confidence: new(0.8)},
		},
		Nodes: []models.TaxonomyResultNode{
			{NodeKey: "root", NodeType: models.TaxonomyNodeTypeRoot, Label: "Feedback", Level: 0},
			{NodeKey: "leaf", ParentKey: new("root"), ClusterKey: new(1), NodeType: models.TaxonomyNodeTypeLeaf, Label: "Login", Level: 1},
		},
	}

	var completed models.TaxonomyRun
	requestTaxonomyJSON(ctx, t, http.MethodPut, harness.server.URL+"/internal/v1/taxonomy/runs/"+runID.String()+"/result",
		harness.internalToken, result, http.StatusOK, &completed)
	require.Equal(t, models.TaxonomyRunStatusSucceeded, completed.Status)

	// 4. Public active tree now reflects the generated taxonomy, and its leaf is editable.
	scopeQuery := url.Values{
		"tenant_id":   {scope.TenantID},
		"source_type": {scope.SourceType},
		"source_id":   {scope.SourceID},
		"field_id":    {scope.FieldID},
	}

	var tree models.TaxonomyTreeResponse
	requestTaxonomyJSON(ctx, t, http.MethodGet,
		taxonomyURL(harness.server.URL, "/v1/taxonomy/runs/active/tree", scopeQuery), harness.apiKey, nil, http.StatusOK, &tree)
	require.Equal(t, runID, tree.Run.ID)

	leaf := findTaxonomyNode(tree.Root, func(n models.TaxonomyNode) bool { return n.NodeType == models.TaxonomyNodeTypeLeaf })
	require.NotNil(t, leaf, "generated tree must contain a leaf node")

	var renamed models.TaxonomyNode
	requestTaxonomyJSON(ctx, t, http.MethodPatch, harness.server.URL+"/v1/taxonomy/nodes/"+leaf.ID.String(), harness.apiKey,
		models.RenameTaxonomyNodeRequest{TenantID: scope.TenantID, ActorID: "curator", Label: "Authentication"},
		http.StatusOK, &renamed)
	assert.Equal(t, "Authentication", renamed.Label)
}

// startRunForScope creates and starts a run through the repository, returning its ID. It is
// used to set up internal-endpoint tests without going through the public create endpoint.
func startRunForScope(ctx context.Context, t *testing.T, harness *taxonomyTestServer, scope models.TaxonomyScope) uuid.UUID {
	t.Helper()

	recordCount, embeddingCount, _, err := harness.repo.CountScopeInput(ctx, scope, taxonomyEmbeddingModel)
	require.NoError(t, err)

	run, created, err := harness.repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{
		TaxonomyScope: scope, RecordCount: recordCount, EmbeddingCount: embeddingCount,
	})
	require.NoError(t, err)
	require.True(t, created)

	_, err = harness.repo.MarkRunRunning(ctx, run.ID, scope.TenantID)
	require.NoError(t, err)

	return run.ID
}

// createRunningRun creates a run in the running state without requiring seeded embeddings,
// for internal endpoints that only need a run in the correct state.
func createRunningRun(ctx context.Context, t *testing.T, harness *taxonomyTestServer, scope models.TaxonomyScope) uuid.UUID {
	t.Helper()

	run, created, err := harness.repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{
		TaxonomyScope: scope, RecordCount: 1, EmbeddingCount: 1,
	})
	require.NoError(t, err)
	require.True(t, created)

	_, err = harness.repo.MarkRunRunning(ctx, run.ID, scope.TenantID)
	require.NoError(t, err)

	return run.ID
}
