package tests

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/api/response"
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

	if wantStatus >= http.StatusBadRequest {
		assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))
	} else {
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	}

	if out != nil {
		require.NoError(t, decodeData(resp, out))
	}
}

// requestTaxonomyProblem asserts the stable RFC 9457 contract used by taxonomy errors.
func requestTaxonomyProblem(
	ctx context.Context,
	t *testing.T,
	method, requestURL, token string,
	body any,
	wantStatus int,
	wantCode, wantType string,
) response.ProblemDetails {
	t.Helper()

	resp := doTaxonomyRequest(ctx, t, method, requestURL, token, body)

	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, wantStatus, resp.StatusCode)
	require.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))

	var problem response.ProblemDetails
	require.NoError(t, decodeData(resp, &problem))
	assert.Equal(t, wantStatus, problem.Status)
	assert.Equal(t, wantCode, problem.Code)
	assert.Equal(t, wantType, problem.Type)
	assert.Equal(t, resp.Request.URL.Path, problem.Instance)
	assert.NotEmpty(t, problem.Title)
	assert.NotEmpty(t, problem.RequestID)

	return problem
}

func assertTaxonomyInvalidParam(t *testing.T, problem response.ProblemDetails, name, reasonContains string) {
	t.Helper()

	require.NotEmpty(t, problem.InvalidParams)

	for _, param := range problem.InvalidParams {
		if param.Name == name || strings.HasSuffix(param.Name, "."+name) {
			assert.Contains(t, param.Reason, reasonContains)

			return
		}
	}

	require.Failf(t, "invalid parameter not found", "wanted %q in %#v", name, problem.InvalidParams)
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
		requestTaxonomyProblem(ctx, t, http.MethodGet, fieldsURL, "", nil,
			http.StatusUnauthorized, response.CodeUnauthorized, response.ProblemTypeUnauthorized)
		requestTaxonomyProblem(ctx, t, http.MethodGet, fieldsURL, "wrong-key", nil,
			http.StatusUnauthorized, response.CodeUnauthorized, response.ProblemTypeUnauthorized)
	})

	t.Run("public endpoint accepts the Hub API key", func(t *testing.T) {
		requestTaxonomyJSON(ctx, t, http.MethodGet, fieldsURL, harness.apiKey, nil, http.StatusOK, nil)
	})

	t.Run("internal endpoint rejects missing and wrong credentials", func(t *testing.T) {
		requestTaxonomyProblem(ctx, t, http.MethodGet, authCheckURL, "", nil,
			http.StatusUnauthorized, response.CodeUnauthorized, response.ProblemTypeUnauthorized)
		requestTaxonomyProblem(ctx, t, http.MethodGet, authCheckURL, "wrong-token", nil,
			http.StatusUnauthorized, response.CodeUnauthorized, response.ProblemTypeUnauthorized)
	})

	t.Run("internal endpoint accepts the internal token", func(t *testing.T) {
		var body map[string]any
		requestTaxonomyJSON(ctx, t, http.MethodGet, authCheckURL, harness.internalToken, nil, http.StatusOK, &body)
		assert.Equal(t, "ok", body["status"])
		assert.Equal(t, "hub-taxonomy-internal", body["service"])
	})

	t.Run("credentials do not cross auth layers", func(t *testing.T) {
		// The internal token must not authorize the public API...
		requestTaxonomyProblem(ctx, t, http.MethodGet, fieldsURL, harness.internalToken, nil,
			http.StatusUnauthorized, response.CodeUnauthorized, response.ProblemTypeUnauthorized)
		// ...and the public API key must not authorize the internal service API.
		requestTaxonomyProblem(ctx, t, http.MethodGet, authCheckURL, harness.apiKey, nil,
			http.StatusUnauthorized, response.CodeUnauthorized, response.ProblemTypeUnauthorized)
	})
}

func TestTaxonomyAPI_RoutingProblems(t *testing.T) {
	ctx := context.Background()
	harness := setupTaxonomyAPIServer(t)

	t.Run("unknown paths use the problem contract", func(t *testing.T) {
		requestTaxonomyProblem(
			ctx,
			t,
			http.MethodGet,
			harness.server.URL+"/v1/taxonomy/not-a-route",
			harness.apiKey,
			nil,
			http.StatusNotFound,
			response.CodeNotFound,
			response.ProblemTypeNotFound,
		)
	})

	t.Run("wrong methods use the problem contract", func(t *testing.T) {
		requestTaxonomyProblem(
			ctx,
			t,
			http.MethodPost,
			harness.server.URL+"/v1/taxonomy/fields",
			harness.apiKey,
			nil,
			http.StatusMethodNotAllowed,
			response.CodeMethodNotAllowed,
			response.ProblemTypeMethodNotAllowed,
		)
	})
}

// TestTaxonomyAPI_PublicReadAndEdit covers the public read and edit endpoints against a
// seeded, activated taxonomy graph.
func TestTaxonomyAPI_PublicReadAndEdit(t *testing.T) {
	ctx := context.Background()
	harness := setupTaxonomyAPIServer(t)

	scope := uniqueTaxonomyScope("tax-api-read")
	ids := seedTaxonomyGraph(ctx, t, harness.db, scope)
	seededRecordIDs := append(
		[]uuid.UUID{ids.FeedbackRecordID},
		seedEmbeddedFeedback(ctx, t, harness, scope, 1)...,
	)

	scopeQuery := url.Values{
		"tenant_id":   {scope.TenantID},
		"source_type": {scope.SourceType},
		"source_id":   {scope.SourceID},
		"field_id":    {scope.FieldID},
	}
	tenantQuery := url.Values{"tenant_id": {scope.TenantID}}

	t.Run("list fields returns the expected scope and counts", func(t *testing.T) {
		var resp models.TaxonomyFieldsResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/fields", tenantQuery),
			harness.apiKey, nil, http.StatusOK, &resp)

		require.Len(t, resp.Data, 1)
		assert.Equal(t, scope.TenantID, resp.Data[0].TenantID)
		assert.Equal(t, scope.SourceType, resp.Data[0].SourceType)
		assert.Equal(t, scope.SourceID, resp.Data[0].SourceID)
		assert.Equal(t, scope.FieldID, resp.Data[0].FieldID)
		assert.Equal(t, len(seededRecordIDs), resp.Data[0].RecordCount)
		assert.Equal(t, 1, resp.Data[0].EmbeddingCount)
	})

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
	seedEmbeddedFeedback(ctx, t, harness, scope, 1)

	otherScope := uniqueTaxonomyScope("tax-api-iso-other")
	otherIDs := seedTaxonomyGraph(ctx, t, harness.db, otherScope)
	seedEmbeddedFeedback(ctx, t, harness, otherScope, 1)

	otherTenant := otherScope.TenantID
	otherQuery := url.Values{"tenant_id": {otherTenant}}
	tenantQuery := url.Values{"tenant_id": {scope.TenantID}}

	t.Run("list endpoints return only the requested tenant", func(t *testing.T) {
		var fields models.TaxonomyFieldsResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/fields", tenantQuery),
			harness.apiKey, nil, http.StatusOK, &fields)
		require.NotEmpty(t, fields.Data)

		for _, field := range fields.Data {
			assert.Equal(t, scope.TenantID, field.TenantID)
			assert.NotEqual(t, otherTenant, field.TenantID)
		}

		var runs models.ListTaxonomyRunsResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/runs", tenantQuery),
			harness.apiKey, nil, http.StatusOK, &runs)
		require.Len(t, runs.Data, 1)
		assert.Equal(t, ids.RunID, runs.Data[0].ID)
		assert.NotEqual(t, otherIDs.RunID, runs.Data[0].ID)
		assert.Equal(t, scope.TenantID, runs.Data[0].TenantID)
	})

	t.Run("get run 404s for another tenant", func(t *testing.T) {
		requestTaxonomyProblem(ctx, t, http.MethodGet,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/runs/"+ids.RunID.String(), otherQuery),
			harness.apiKey, nil, http.StatusNotFound, response.CodeNotFound, response.ProblemTypeNotFound)
	})

	t.Run("get tree 404s for another tenant", func(t *testing.T) {
		treeURL := taxonomyURL(harness.server.URL, "/v1/taxonomy/runs/"+ids.RunID.String()+"/tree", otherQuery)
		requestTaxonomyProblem(ctx, t, http.MethodGet, treeURL, harness.apiKey, nil,
			http.StatusNotFound, response.CodeNotFound, response.ProblemTypeNotFound)
	})

	t.Run("rename 404s for another tenant", func(t *testing.T) {
		body := models.RenameTaxonomyNodeRequest{TenantID: otherTenant, ActorID: "attacker", Label: "Hijacked"}
		requestTaxonomyProblem(ctx, t, http.MethodPatch,
			harness.server.URL+"/v1/taxonomy/nodes/"+ids.BranchID.String(), harness.apiKey, body,
			http.StatusNotFound, response.CodeNotFound, response.ProblemTypeNotFound)
	})

	t.Run("remove 404s for another tenant", func(t *testing.T) {
		removeQuery := url.Values{"tenant_id": {otherTenant}, "actor_id": {"attacker"}}
		requestTaxonomyProblem(ctx, t, http.MethodDelete,
			taxonomyURL(harness.server.URL, "/v1/taxonomy/nodes/"+ids.BranchID.String(), removeQuery),
			harness.apiKey, nil, http.StatusNotFound, response.CodeNotFound, response.ProblemTypeNotFound)
	})

	t.Run("node records 404 for another tenant", func(t *testing.T) {
		// Not 200-with-empty: that would make a foreign node indistinguishable from one of your own
		// that holds no records. Same contract as the run, tree, rename and remove cases above.
		recordsURL := taxonomyURL(harness.server.URL, "/v1/taxonomy/nodes/"+ids.RootID.String()+"/records", otherQuery)
		requestTaxonomyJSON(ctx, t, http.MethodGet, recordsURL, harness.apiKey, nil, http.StatusNotFound, nil)
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
	// prove we count distinct records, not nodes or clusters. The cluster now holds two records.
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

	// Add a SECOND leaf under the same branch that references the SAME cluster as the first leaf.
	// The schema does not forbid this, and it is the case where a naive per-cluster sum would
	// double count: the branch would report 4 instead of the 2 distinct records. This locks in the
	// distinct-count behaviour.
	var sharedClusterLeafID uuid.UUID

	err = harness.db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (run_id, parent_id, cluster_id, node_type, label, original_label, level, sort_order)
		VALUES ($1, $2, $3, 'leaf'::taxonomy_node_type_enum, 'Login Problems (dup)', 'Login Problems (dup)', 2, 1)
		RETURNING id`,
		ids.RunID, ids.BranchID, ids.ClusterID,
	).Scan(&sharedClusterLeafID)
	require.NoError(t, err)

	tenantQuery := url.Values{"tenant_id": {scope.TenantID}}
	countsURL := func(runID string, q url.Values) string {
		return taxonomyURL(harness.server.URL, "/v1/taxonomy/runs/"+runID+"/record-counts", q)
	}

	t.Run("returns distinct subtree totals per node", func(t *testing.T) {
		var resp models.TaxonomyRecordCountsResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet, countsURL(ids.RunID.String(), tenantQuery),
			harness.apiKey, nil, http.StatusOK, &resp)

		byNode := make(map[uuid.UUID]int64, len(resp.Counts))
		for _, c := range resp.Counts {
			byNode[c.NodeID] = c.RecordCount
		}

		// Seed graph is root -> branch -> {leaf, sharedClusterLeaf}; both leaves point at the one
		// cluster, which holds two records. Each leaf reports 2. The branch and root carry no
		// cluster of their own, so their counts come purely from the subtree — and must be 2
		// distinct records, not 4 (which double counting the shared cluster would produce).
		assert.Equal(t, int64(2), byNode[ids.LeafID], "leaf reports its cluster's two records")
		assert.Equal(t, int64(2), byNode[sharedClusterLeafID], "shared-cluster leaf reports the same two")
		assert.Equal(t, int64(2), byNode[ids.BranchID], "branch counts distinct records, not per-node sums")
		assert.Equal(t, int64(2), byNode[ids.RootID], "root reports the run's distinct total")
	})

	t.Run("404s for another tenant", func(t *testing.T) {
		otherQuery := url.Values{"tenant_id": {"tax-api-count-other-" + uuid.NewString()}}
		requestTaxonomyProblem(ctx, t, http.MethodGet, countsURL(ids.RunID.String(), otherQuery),
			harness.apiKey, nil, http.StatusNotFound, response.CodeNotFound, response.ProblemTypeNotFound)
	})

	t.Run("404s for an unknown run", func(t *testing.T) {
		requestTaxonomyProblem(ctx, t, http.MethodGet, countsURL(uuid.NewString(), tenantQuery),
			harness.apiKey, nil, http.StatusNotFound, response.CodeNotFound, response.ProblemTypeNotFound)
	})

	t.Run("400s for an invalid run ID", func(t *testing.T) {
		problem := requestTaxonomyProblem(ctx, t, http.MethodGet, countsURL("not-a-uuid", tenantQuery),
			harness.apiKey, nil, http.StatusBadRequest, response.CodeValidation, response.ProblemTypeValidation)
		assertTaxonomyInvalidParam(t, problem, "run_id", "valid UUID")
	})

	t.Run("400s when tenant_id is missing", func(t *testing.T) {
		problem := requestTaxonomyProblem(ctx, t, http.MethodGet, countsURL(ids.RunID.String(), url.Values{}),
			harness.apiKey, nil, http.StatusBadRequest, response.CodeValidation, response.ProblemTypeValidation)
		assertTaxonomyInvalidParam(t, problem, "tenant_id", "required")
	})
}

// TestTaxonomyAPI_CreateRun covers the public run-creation endpoint: validation failures,
// insufficient embedded input, the accepted happy path, and in-progress reuse.
func TestTaxonomyAPI_CreateRun(t *testing.T) {
	ctx := context.Background()
	harness := setupTaxonomyAPIServer(t)
	runsURL := harness.server.URL + "/v1/taxonomy/runs"

	t.Run("missing scope fields are rejected", func(t *testing.T) {
		problem := requestTaxonomyProblem(ctx, t, http.MethodPost, runsURL, harness.apiKey,
			map[string]any{"source_type": "formbricks"}, http.StatusBadRequest,
			response.CodeValidation, response.ProblemTypeValidation)
		assertTaxonomyInvalidParam(t, problem, "tenant_id", "required")
	})

	t.Run("scope without text records is rejected", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-api-create-empty")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)

		body := models.CreateTaxonomyRunRequest{TaxonomyScope: scope}
		problem := requestTaxonomyProblem(ctx, t, http.MethodPost, runsURL, harness.apiKey, body,
			http.StatusBadRequest, response.CodeValidation, response.ProblemTypeValidation)
		assertTaxonomyInvalidParam(t, problem, "field_id", "no text feedback records")
	})

	t.Run("scope with missing embeddings is rejected", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-api-create-no-embeddings")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		insertScopeFeedbackRecord(ctx, t, harness.db, scope)

		body := models.CreateTaxonomyRunRequest{TaxonomyScope: scope}
		problem := requestTaxonomyProblem(ctx, t, http.MethodPost, runsURL, harness.apiKey, body,
			http.StatusBadRequest, response.CodeValidation, response.ProblemTypeValidation)
		assertTaxonomyInvalidParam(t, problem, "field_id", "found 0")
	})

	t.Run("scope below the minimum embedded record count is rejected", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-api-create-insufficient")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		seededRecordIDs := seedEmbeddedFeedback(ctx, t, harness, scope, taxonomyMinEmbeddedRecords-1)

		body := models.CreateTaxonomyRunRequest{TaxonomyScope: scope}
		problem := requestTaxonomyProblem(ctx, t, http.MethodPost, runsURL, harness.apiKey, body,
			http.StatusBadRequest, response.CodeValidation, response.ProblemTypeValidation)
		assertTaxonomyInvalidParam(t, problem, "field_id", fmt.Sprintf("found %d", len(seededRecordIDs)))
	})

	t.Run("directory scope rejects field selectors", func(t *testing.T) {
		scope := uniqueDirectoryTaxonomyScope("tax-api-create-directory-invalid")
		scope.SourceType = "formbricks"

		body := models.CreateTaxonomyRunRequest{TaxonomyScope: scope}
		problem := requestTaxonomyProblem(ctx, t, http.MethodPost, runsURL, harness.apiKey, body,
			http.StatusBadRequest, response.CodeValidation, response.ProblemTypeValidation)
		assertTaxonomyInvalidParam(t, problem, "source_type", "empty")
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

// TestTaxonomyAPI_ServiceAvailability locks in the documented 503 contracts when Hub
// embeddings or the external taxonomy compute service are not configured.
func TestTaxonomyAPI_ServiceAvailability(t *testing.T) {
	t.Run("missing embedding model rejects public and internal input endpoints", func(t *testing.T) {
		harness := setupTaxonomyAPIServer(t, withTaxonomyEmbeddingModel(""))
		ctx := context.Background()
		scope := uniqueTaxonomyScope("tax-api-unconfigured-embeddings")
		runID := createRunningRun(ctx, t, harness, scope)

		fieldsURL := taxonomyURL(
			harness.server.URL,
			"/v1/taxonomy/fields",
			url.Values{"tenant_id": {scope.TenantID}},
		)
		requestTaxonomyProblem(ctx, t, http.MethodGet, fieldsURL, harness.apiKey, nil,
			http.StatusServiceUnavailable, response.CodeServiceUnavailable, response.ProblemTypeServiceUnavailable)

		inputURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/input"
		requestTaxonomyProblem(ctx, t, http.MethodGet, inputURL, harness.internalToken, nil,
			http.StatusServiceUnavailable, response.CodeServiceUnavailable, response.ProblemTypeServiceUnavailable)
	})

	t.Run("missing taxonomy starter rejects run creation", func(t *testing.T) {
		harness := setupTaxonomyAPIServer(t, withoutTaxonomyStarter())
		ctx := context.Background()
		scope := uniqueTaxonomyScope("tax-api-unconfigured-starter")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		seedEmbeddedFeedback(ctx, t, harness, scope, taxonomyMinEmbeddedRecords)

		requestTaxonomyProblem(
			ctx,
			t,
			http.MethodPost,
			harness.server.URL+"/v1/taxonomy/runs",
			harness.apiKey,
			models.CreateTaxonomyRunRequest{TaxonomyScope: scope},
			http.StatusServiceUnavailable,
			response.CodeServiceUnavailable,
			response.ProblemTypeServiceUnavailable,
		)
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
		requestTaxonomyProblem(ctx, t, http.MethodGet, inputURL, "", nil,
			http.StatusUnauthorized, response.CodeUnauthorized, response.ProblemTypeUnauthorized)

		var input models.TaxonomyRunInputResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet, inputURL, harness.internalToken, nil, http.StatusOK, &input)
		assert.Equal(t, runID, input.Run.ID)
		require.NotEmpty(t, input.Records)
		assert.NotEmpty(t, input.Records[0].Embedding)
		assert.NotEmpty(t, input.Records[0].ValueText)
	})

	t.Run("run input selection metadata reflects records actually returned", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-input-selection")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		seedEmbeddedFeedback(ctx, t, harness, scope, 1)
		insertScopeFeedbackRecord(ctx, t, harness.db, scope)

		runID := startRunForScope(ctx, t, harness, scope)
		inputURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/input"

		var input models.TaxonomyRunInputResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet, inputURL, harness.internalToken, nil, http.StatusOK, &input)
		require.Len(t, input.Records, 1)
		assert.Equal(t, 2, input.Run.EligibleCount)
		assert.Equal(t, 1, input.Run.SelectedCount)
		assert.Equal(t, repository.MaxTaxonomyRunInputRows, input.Run.SelectionCap)
		assert.True(t, input.Run.SelectionTruncated)
		assert.Equal(t, "most_recent", input.Run.SelectionStrategy)
	})

	t.Run("get run input never includes another tenant's matching records", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-input-tenant")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		ownedRecordIDs := seedEmbeddedFeedback(ctx, t, harness, scope, taxonomyMinEmbeddedRecords)

		otherScope := scope
		otherScope.TenantID = "tax-internal-input-other-" + uuid.NewString()
		cleanupTaxonomyTenant(ctx, t, harness.db, otherScope.TenantID)
		otherRecordIDs := seedEmbeddedFeedback(ctx, t, harness, otherScope, taxonomyMinEmbeddedRecords)

		runID := startRunForScope(ctx, t, harness, scope)
		inputURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/input"

		var input models.TaxonomyRunInputResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet, inputURL, harness.internalToken, nil, http.StatusOK, &input)

		actualRecordIDs := make(map[uuid.UUID]struct{}, len(input.Records))
		for _, record := range input.Records {
			actualRecordIDs[record.FeedbackRecordID] = struct{}{}
		}

		for _, recordID := range ownedRecordIDs {
			assert.Contains(t, actualRecordIDs, recordID)
		}

		for _, recordID := range otherRecordIDs {
			assert.NotContains(t, actualRecordIDs, recordID)
		}
	})

	t.Run("run input returns translated text when present", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-translated-input")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)

		original := "La machine ne demarre pas"
		translated := "The machine does not start"

		var recordID uuid.UUID

		err := harness.db.QueryRow(ctx, `
			INSERT INTO feedback_records (
				source_type, source_id, field_id, field_label, field_type,
				value_text, value_text_translated, translation_lang_key, tenant_id, submission_id
			)
			VALUES ($1, $2, $3, 'Feedback', 'text'::field_type_enum, $4, $5, 'en-US', $6, $7)
			RETURNING id`,
			scope.SourceType, scope.SourceID, scope.FieldID, original, translated, scope.TenantID, "submission-"+uuid.NewString(),
		).Scan(&recordID)
		require.NoError(t, err)

		embedding := make([]float32, models.EmbeddingVectorDimensions)
		embedding[0] = 0.25
		require.NoError(t, harness.embeddingsRepo.Upsert(ctx, recordID, taxonomyEmbeddingModel, embedding, nil))

		runID := startRunForScope(ctx, t, harness, scope)
		inputURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/input"

		var input models.TaxonomyRunInputResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet, inputURL, harness.internalToken, nil, http.StatusOK, &input)
		require.Len(t, input.Records, 1)
		assert.Equal(t, translated, input.Records[0].ValueText)
	})

	t.Run("run input includes translated-only records", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-translated-only-input")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)

		translated := "The machine does not start"

		var recordID uuid.UUID

		err := harness.db.QueryRow(ctx, `
			INSERT INTO feedback_records (
				source_type, source_id, field_id, field_label, field_type,
				value_text, value_text_translated, translation_lang_key, tenant_id, submission_id
			)
			VALUES ($1, $2, $3, 'Feedback', 'text'::field_type_enum, NULL, $4, 'en-US', $5, $6)
			RETURNING id`,
			scope.SourceType, scope.SourceID, scope.FieldID, translated, scope.TenantID, "submission-"+uuid.NewString(),
		).Scan(&recordID)
		require.NoError(t, err)

		embedding := make([]float32, models.EmbeddingVectorDimensions)
		embedding[0] = 0.25
		require.NoError(t, harness.embeddingsRepo.Upsert(ctx, recordID, taxonomyEmbeddingModel, embedding, nil))

		recordCount, embeddingCount, _, err := harness.repo.CountScopeInput(ctx, scope, taxonomyEmbeddingModel)
		require.NoError(t, err)
		assert.Equal(t, 1, recordCount)
		assert.Equal(t, 1, embeddingCount)

		runID := startRunForScope(ctx, t, harness, scope)
		inputURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/input"

		var input models.TaxonomyRunInputResponse
		requestTaxonomyJSON(ctx, t, http.MethodGet, inputURL, harness.internalToken, nil, http.StatusOK, &input)
		require.Len(t, input.Records, 1)
		assert.Equal(t, translated, input.Records[0].ValueText)
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
		selectedRecordIDs := make([]uuid.UUID, 0, len(input.Records))

		for _, record := range input.Records {
			fieldIDs[record.FieldID] = true
			sourceTypes[record.SourceType] = true
			selectedRecordIDs = append(selectedRecordIDs, record.FeedbackRecordID)
			assert.NotEmpty(t, record.Embedding)
			assert.NotEmpty(t, record.ValueText)
		}

		assert.True(t, fieldIDs["ces_comment"])
		assert.True(t, fieldIDs["support_comment"])
		assert.True(t, sourceTypes["formbricks"])
		assert.True(t, sourceTypes["support"])

		// Feedback can arrive while a long taxonomy generation is in flight. The completion
		// contract must remain the exact cross-field directory snapshot returned above; the new,
		// more-recent row must neither become required nor displace an already-selected row.
		seedEmbeddedFeedback(ctx, t, harness, firstFieldScope, 1)

		resultURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/result"

		var completed models.TaxonomyRun
		requestTaxonomyJSON(ctx, t, http.MethodPut, resultURL, harness.internalToken,
			validTaxonomyResultForRecords(selectedRecordIDs), http.StatusOK, &completed)
		assert.Equal(t, models.TaxonomyRunStatusSucceeded, completed.Status)
	})

	t.Run("complete run stores artifacts and activates", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-complete")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)

		feedbackRecordID := seedEmbeddedFeedback(ctx, t, harness, scope, 1)[0]
		runID := startRunForScope(ctx, t, harness, scope)

		result := validTaxonomyResult(feedbackRecordID)

		resultURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/result"

		// Auth is required.
		requestTaxonomyProblem(ctx, t, http.MethodPut, resultURL, "", result,
			http.StatusUnauthorized, response.CodeUnauthorized, response.ProblemTypeUnauthorized)

		materializeTaxonomyRunInput(ctx, t, harness, runID)

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
			Diagnostics: &models.TaxonomyRunFailureDiagnostics{
				Phase: "clustering",
				PartialMetrics: &models.TaxonomyRunPartialMetrics{
					SelectedRecordCount:    9995,
					ClusterCount:           80,
					MembershipCount:        9995,
					ClusterCapActivated:    new(true),
					ClusterCapMergeCount:   2,
					LabelFallbackCount:     1,
					PostProcessingFailed:   new(false),
					ClusteringFallbackUsed: new(false),
				},
				PhaseDurations: map[string]float64{
					"input_fetch": 0.5, "input_validation": 0.1, "clustering": 2.5,
					"evidence_selection": 0.2, "cluster_labeling": 1.2, "taxonomy_generation": 3.4,
					"payload_validation": 0.3, "persistence": 0.7,
				},
			},
		}
		failedURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/failed"

		// Auth is required.
		requestTaxonomyProblem(ctx, t, http.MethodPost, failedURL, "", body,
			http.StatusUnauthorized, response.CodeUnauthorized, response.ProblemTypeUnauthorized)

		var run models.TaxonomyRun
		requestTaxonomyJSON(ctx, t, http.MethodPost, failedURL, harness.internalToken, body, http.StatusOK, &run)
		assert.Equal(t, models.TaxonomyRunStatusFailed, run.Status)
		require.NotNil(t, run.Error)
		assert.Equal(t, "clustering did not converge", *run.Error)
		assert.JSONEq(t,
			`{"failure_diagnostics":{"phase":"clustering","partial_metrics":{`+
				`"selected_record_count":9995,"cluster_count":80,"membership_count":9995,`+
				`"clustering_fallback_used":false,"cluster_cap_activated":true,`+
				`"cluster_cap_merge_count":2,"post_processing_failed":false,"label_fallback_count":1},`+
				`"phase_durations_seconds":{`+
				`"input_fetch":0.5,"input_validation":0.1,"clustering":2.5,"evidence_selection":0.2,`+
				`"cluster_labeling":1.2,"taxonomy_generation":3.4,"payload_validation":0.3,"persistence":0.7}}}`,
			string(run.Metrics),
		)
	})

	t.Run("heartbeat returns the internal wire contract", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-heartbeat")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		runID := createRunningRun(ctx, t, harness, scope)
		heartbeatURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/heartbeat"

		requestTaxonomyProblem(ctx, t, http.MethodPost, heartbeatURL, "", nil,
			http.StatusUnauthorized, response.CodeUnauthorized, response.ProblemTypeUnauthorized)

		var body map[string]string
		requestTaxonomyJSON(
			ctx,
			t,
			http.MethodPost,
			heartbeatURL,
			harness.internalToken,
			nil,
			http.StatusOK,
			&body,
		)
		assert.Equal(t, map[string]string{"status": "ok"}, body)
	})
}

// TestTaxonomyAPI_InternalErrors covers missing runs, malformed identifiers, and the
// idempotent-versus-conflicting terminal-state contract of the internal taxonomy service.
func TestTaxonomyAPI_InternalErrors(t *testing.T) {
	ctx := context.Background()
	harness := setupTaxonomyAPIServer(t)
	unknownRunID := uuid.New()
	failedBody := models.TaxonomyRunFailedRequest{
		Error:     "generation failed",
		ErrorCode: models.TaxonomyRunFailureCodeGenerationFailed,
	}
	resultBody := validTaxonomyResult(uuid.New())

	t.Run("result payload validation is machine readable", func(t *testing.T) {
		problem := requestTaxonomyProblem(
			ctx,
			t,
			http.MethodPut,
			harness.server.URL+"/internal/v1/taxonomy/runs/"+unknownRunID.String()+"/result",
			harness.internalToken,
			map[string]any{"clusters": []any{}},
			http.StatusBadRequest,
			response.CodeValidation,
			response.ProblemTypeValidation,
		)
		assertTaxonomyInvalidParam(t, problem, "memberships", "required")
	})

	t.Run("invalid generated topology and membership coverage are rejected before persistence", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-invalid-output")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		feedbackRecordID := insertScopeFeedbackRecord(ctx, t, harness.db, scope)
		runID := createRunningRun(ctx, t, harness, scope)
		resultURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/result"

		duplicateMembership := validTaxonomyResult(feedbackRecordID)
		duplicateMembership.Clusters[0].Size = 2
		duplicateMembership.Memberships = append(
			duplicateMembership.Memberships,
			duplicateMembership.Memberships[0],
		)
		requestTaxonomyProblem(
			ctx, t, http.MethodPut, resultURL, harness.internalToken, duplicateMembership,
			http.StatusBadRequest, response.CodeValidation, response.ProblemTypeValidation,
		)

		invalidDepth := validTaxonomyResult(feedbackRecordID)
		invalidDepth.Nodes[len(invalidDepth.Nodes)-1].Level = 3
		requestTaxonomyProblem(
			ctx, t, http.MethodPut, resultURL, harness.internalToken, invalidDepth,
			http.StatusBadRequest, response.CodeValidation, response.ProblemTypeValidation,
		)

		assertTaxonomyRunUnchanged(ctx, t, harness, runID, scope.TenantID)
	})

	t.Run("incomplete selected input coverage is rejected before persistence", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-incomplete-coverage")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		feedbackRecordIDs := seedEmbeddedFeedback(ctx, t, harness, scope, 2)
		runID := startRunForScope(ctx, t, harness, scope)
		materializeTaxonomyRunInput(ctx, t, harness, runID)
		resultURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/result"

		problem := requestTaxonomyProblem(
			ctx,
			t,
			http.MethodPut,
			resultURL,
			harness.internalToken,
			validTaxonomyResult(feedbackRecordIDs[0]),
			http.StatusBadRequest,
			response.CodeValidation,
			response.ProblemTypeValidation,
		)
		assertTaxonomyInvalidParam(t, problem, "memberships", "selected run input")
		assertTaxonomyRunUnchanged(ctx, t, harness, runID, scope.TenantID)
	})

	t.Run("failed payload validation is machine readable", func(t *testing.T) {
		problem := requestTaxonomyProblem(
			ctx,
			t,
			http.MethodPost,
			harness.server.URL+"/internal/v1/taxonomy/runs/"+unknownRunID.String()+"/failed",
			harness.internalToken,
			map[string]any{"error_code": models.TaxonomyRunFailureCodeGenerationFailed},
			http.StatusBadRequest,
			response.CodeValidation,
			response.ProblemTypeValidation,
		)
		assertTaxonomyInvalidParam(t, problem, "error", "required")
	})

	t.Run("failed diagnostics phase durations are bounded", func(t *testing.T) {
		tests := []struct {
			name      string
			durations map[string]float64
		}{
			{name: "disallowed key", durations: map[string]float64{"prompt_rendering": 1}},
			{name: "negative duration", durations: map[string]float64{"clustering": -0.1}},
			{
				name: "too many entries",
				durations: map[string]float64{
					"input_fetch": 1, "input_validation": 1, "clustering": 1, "evidence_selection": 1,
					"cluster_labeling": 1, "taxonomy_generation": 1, "payload_validation": 1,
					"persistence": 1, "prompt_rendering": 1,
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				requestTaxonomyProblem(
					ctx,
					t,
					http.MethodPost,
					harness.server.URL+"/internal/v1/taxonomy/runs/"+unknownRunID.String()+"/failed",
					harness.internalToken,
					models.TaxonomyRunFailedRequest{
						Error:     "generation failed",
						ErrorCode: models.TaxonomyRunFailureCodeGenerationFailed,
						Diagnostics: &models.TaxonomyRunFailureDiagnostics{
							PhaseDurations: test.durations,
						},
					},
					http.StatusBadRequest,
					response.CodeValidation,
					response.ProblemTypeValidation,
				)
			})
		}
	})

	t.Run("failed diagnostics nested counts are bounded", func(t *testing.T) {
		problem := requestTaxonomyProblem(
			ctx,
			t,
			http.MethodPost,
			harness.server.URL+"/internal/v1/taxonomy/runs/"+unknownRunID.String()+"/failed",
			harness.internalToken,
			models.TaxonomyRunFailedRequest{
				Error:     "generation failed",
				ErrorCode: models.TaxonomyRunFailureCodeGenerationFailed,
				Diagnostics: &models.TaxonomyRunFailureDiagnostics{
					PartialMetrics: &models.TaxonomyRunPartialMetrics{ClusterCount: 5000},
				},
			},
			http.StatusBadRequest,
			response.CodeValidation,
			response.ProblemTypeValidation,
		)
		assertTaxonomyInvalidParam(t, problem, "cluster_count", "1000")
	})

	tests := []struct {
		name   string
		method string
		suffix string
		body   any
	}{
		{name: "input", method: http.MethodGet, suffix: "/input"},
		{name: "result", method: http.MethodPut, suffix: "/result", body: resultBody},
		{name: "failed", method: http.MethodPost, suffix: "/failed", body: failedBody},
		{name: "heartbeat", method: http.MethodPost, suffix: "/heartbeat"},
	}

	for _, test := range tests {
		t.Run(test.name+" returns 404 for an unknown run", func(t *testing.T) {
			requestTaxonomyProblem(
				ctx,
				t,
				test.method,
				harness.server.URL+"/internal/v1/taxonomy/runs/"+unknownRunID.String()+test.suffix,
				harness.internalToken,
				test.body,
				http.StatusNotFound,
				response.CodeNotFound,
				response.ProblemTypeNotFound,
			)
		})

		t.Run(test.name+" returns 400 for a malformed run ID", func(t *testing.T) {
			problem := requestTaxonomyProblem(
				ctx,
				t,
				test.method,
				harness.server.URL+"/internal/v1/taxonomy/runs/not-a-uuid"+test.suffix,
				harness.internalToken,
				test.body,
				http.StatusBadRequest,
				response.CodeValidation,
				response.ProblemTypeValidation,
			)
			assertTaxonomyInvalidParam(t, problem, "run_id", "valid UUID")
		})
	}

	t.Run("repeating an identical result is idempotent but a different result conflicts", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-complete-conflict")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		feedbackRecordID := seedEmbeddedFeedback(ctx, t, harness, scope, 1)[0]
		runID := startRunForScope(ctx, t, harness, scope)
		materializeTaxonomyRunInput(ctx, t, harness, runID)

		result := validTaxonomyResult(feedbackRecordID)
		resultURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/result"

		var completed models.TaxonomyRun
		requestTaxonomyJSON(
			ctx,
			t,
			http.MethodPut,
			resultURL,
			harness.internalToken,
			result,
			http.StatusOK,
			&completed,
		)
		require.Equal(t, models.TaxonomyRunStatusSucceeded, completed.Status)

		var repeated models.TaxonomyRun
		requestTaxonomyJSON(
			ctx,
			t,
			http.MethodPut,
			resultURL,
			harness.internalToken,
			result,
			http.StatusOK,
			&repeated,
		)
		require.Equal(t, completed.ID, repeated.ID)
		require.Equal(t, models.TaxonomyRunStatusSucceeded, repeated.Status)

		differentResult := result
		differentResult.Nodes = append([]models.TaxonomyResultNode(nil), result.Nodes...)
		differentResult.Nodes[1].Label = "Different login theme"
		requestTaxonomyProblem(ctx, t, http.MethodPut, resultURL, harness.internalToken, differentResult,
			http.StatusConflict, response.CodeConflict, response.ProblemTypeConflict)
	})

	t.Run("repeating an identical failure is idempotent but a different failure conflicts", func(t *testing.T) {
		scope := uniqueTaxonomyScope("tax-internal-fail-conflict")
		cleanupTaxonomyTenant(ctx, t, harness.db, scope.TenantID)
		runID := createRunningRun(ctx, t, harness, scope)
		failedURL := harness.server.URL + "/internal/v1/taxonomy/runs/" + runID.String() + "/failed"

		var failed models.TaxonomyRun
		requestTaxonomyJSON(
			ctx,
			t,
			http.MethodPost,
			failedURL,
			harness.internalToken,
			failedBody,
			http.StatusOK,
			&failed,
		)
		require.Equal(t, models.TaxonomyRunStatusFailed, failed.Status)

		var repeated models.TaxonomyRun
		requestTaxonomyJSON(
			ctx,
			t,
			http.MethodPost,
			failedURL,
			harness.internalToken,
			failedBody,
			http.StatusOK,
			&repeated,
		)
		require.Equal(t, failed.ID, repeated.ID)
		require.Equal(t, models.TaxonomyRunStatusFailed, repeated.Status)

		differentFailure := failedBody
		differentFailure.Error = "a different terminal failure"
		requestTaxonomyProblem(ctx, t, http.MethodPost, failedURL, harness.internalToken, differentFailure,
			http.StatusConflict, response.CodeConflict, response.ProblemTypeConflict)
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

	// 3. Internal service posts a structurally complete generated result covering every input record.
	memberships := make([]models.TaxonomyResultMembership, 0, len(input.Records))
	for _, record := range input.Records {
		memberships = append(memberships, models.TaxonomyResultMembership{
			ClusterKey: 1, FeedbackRecordID: record.FeedbackRecordID, Confidence: new(0.8),
		})
	}

	result := models.TaxonomyRunResultRequest{
		Clusters:    []models.TaxonomyResultCluster{{ClusterKey: 1, Label: new("login"), Size: len(input.Records)}},
		Memberships: memberships,
		Nodes: []models.TaxonomyResultNode{
			{NodeKey: "root", NodeType: models.TaxonomyNodeTypeRoot, Label: "Feedback", Level: 0},
			{
				NodeKey: "level-2", ParentKey: new("root"), NodeType: models.TaxonomyNodeTypeBranch,
				Label: "Experience", Level: 1,
			},
			{
				NodeKey: "level-3", ParentKey: new("level-2"), NodeType: models.TaxonomyNodeTypeBranch,
				Label: "Account Access", Level: 2,
			},
			{
				NodeKey: "level-4", ParentKey: new("level-3"), NodeType: models.TaxonomyNodeTypeBranch,
				Label: "Authentication", Level: 3,
			},
			{
				NodeKey: "leaf", ParentKey: new("level-4"), ClusterKey: new(1),
				NodeType: models.TaxonomyNodeTypeLeaf, Label: "Login", Level: 4,
			},
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

func materializeTaxonomyRunInput(
	ctx context.Context,
	t *testing.T,
	harness *taxonomyTestServer,
	runID uuid.UUID,
) {
	t.Helper()

	var input models.TaxonomyRunInputResponse
	requestTaxonomyJSON(ctx, t, http.MethodGet,
		harness.server.URL+"/internal/v1/taxonomy/runs/"+runID.String()+"/input",
		harness.internalToken, nil, http.StatusOK, &input)
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

func assertTaxonomyRunUnchanged(
	ctx context.Context,
	t *testing.T,
	harness *taxonomyTestServer,
	runID uuid.UUID,
	tenantID string,
) {
	t.Helper()

	run, err := harness.repo.GetRunForTenant(ctx, runID, tenantID)
	require.NoError(t, err)
	assert.Equal(t, models.TaxonomyRunStatusRunning, run.Status)
	assert.Zero(t, run.ClusterCount)
	assert.Zero(t, run.NodeCount)
	assert.Equal(t, int64(0), countTenantDataRows(
		ctx, t, harness.db, `SELECT COUNT(*) FROM taxonomy_clusters WHERE run_id = $1`, runID,
	))
	assert.Equal(t, int64(0), countTenantDataRows(
		ctx, t, harness.db, `SELECT COUNT(*) FROM taxonomy_cluster_memberships WHERE run_id = $1`, runID,
	))
	assert.Equal(t, int64(0), countTenantDataRows(
		ctx, t, harness.db, `SELECT COUNT(*) FROM taxonomy_nodes WHERE run_id = $1`, runID,
	))
	assert.Equal(t, int64(0), countTenantDataRows(
		ctx, t, harness.db, `SELECT COUNT(*) FROM taxonomy_active_runs WHERE run_id = $1`, runID,
	))
}

func validTaxonomyResult(feedbackRecordID uuid.UUID) models.TaxonomyRunResultRequest {
	return validTaxonomyResultForRecords([]uuid.UUID{feedbackRecordID})
}

func validTaxonomyResultForRecords(feedbackRecordIDs []uuid.UUID) models.TaxonomyRunResultRequest {
	memberships := make([]models.TaxonomyResultMembership, 0, len(feedbackRecordIDs))
	for _, feedbackRecordID := range feedbackRecordIDs {
		memberships = append(memberships, models.TaxonomyResultMembership{
			ClusterKey: 1, FeedbackRecordID: feedbackRecordID, Confidence: new(0.9),
		})
	}

	return models.TaxonomyRunResultRequest{
		Clusters: []models.TaxonomyResultCluster{
			{ClusterKey: 1, Label: new("login"), Size: len(feedbackRecordIDs)},
		},
		Memberships: memberships,
		Nodes: []models.TaxonomyResultNode{
			{
				NodeKey:  "root",
				NodeType: models.TaxonomyNodeTypeRoot,
				Label:    "Feedback",
				Level:    0,
			},
			{
				NodeKey:   "level-2",
				ParentKey: new("root"),
				NodeType:  models.TaxonomyNodeTypeBranch,
				Label:     "Customer Experience",
				Level:     1,
			},
			{
				NodeKey:   "level-3",
				ParentKey: new("level-2"),
				NodeType:  models.TaxonomyNodeTypeBranch,
				Label:     "Account Experience",
				Level:     2,
			},
			{
				NodeKey:   "level-4",
				ParentKey: new("level-3"),
				NodeType:  models.TaxonomyNodeTypeBranch,
				Label:     "Authentication",
				Level:     3,
			},
			{
				NodeKey:    "leaf",
				ParentKey:  new("level-4"),
				ClusterKey: new(1),
				NodeType:   models.TaxonomyNodeTypeLeaf,
				Label:      "Login",
				Level:      4,
			},
		},
	}
}
