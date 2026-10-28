package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/models"
)

// ENG-3420: a query parameter Hub does not know used to be dropped, so a misspelled filter, or a
// filter only a newer Hub has (updated_since sent to Hub 0.8.x), answered 200 with every record.
// Through the real router, each such parameter is now a 400 naming it, and nothing is listed.
func TestUnknownQueryParamsAreRejected(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{Timeout: 30 * time.Second}
	tenant := "unknown-params-" + uuid.NewString()

	createBody := fmt.Sprintf(`{"source_type":"survey","field_id":"q1","field_type":"text","value_text":"x",`+
		`"submission_id":%q,"tenant_id":%q}`, uuid.NewString(), tenant)
	createReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		server.URL+"/v1/feedback-records", bytes.NewBufferString(createBody))
	require.NoError(t, err)
	createReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	createReq.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(createReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	require.NoError(t, createResp.Body.Close())

	// get returns the status, the decoded problem when the request failed, and the raw body.
	get := func(t *testing.T, path string, query url.Values) (int, response.ProblemDetails, []byte) {
		t.Helper()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			server.URL+path+"?"+query.Encode(), http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)

		defer func() { require.NoError(t, resp.Body.Close()) }()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var problem response.ProblemDetails

		if resp.StatusCode >= http.StatusBadRequest {
			assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))
			require.NoError(t, json.Unmarshal(body, &problem))
		}

		return resp.StatusCode, problem, body
	}

	invalidParamNames := func(problem response.ProblemDetails) []string {
		names := make([]string, 0, len(problem.InvalidParams))
		for _, param := range problem.InvalidParams {
			names = append(names, param.Name)
		}

		return names
	}

	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)

	t.Run("list rejects a misspelled filter instead of returning every record", func(t *testing.T) {
		status, problem, _ := get(t, "/v1/feedback-records", url.Values{
			"tenant_id": {tenant}, "updatd_since": {future},
		})

		require.Equal(t, http.StatusBadRequest, status)
		assert.Equal(t, []string{"updatd_since"}, invalidParamNames(problem))
	})

	t.Run("list still applies the filter it was meant to be", func(t *testing.T) {
		status, _, raw := get(t, "/v1/feedback-records", url.Values{
			"tenant_id": {tenant}, "updated_since": {future}, "sort": {"updated_at"}, "order": {"asc"},
		})
		require.Equal(t, http.StatusOK, status)

		var body models.ListFeedbackRecordsResponse
		require.NoError(t, json.Unmarshal(raw, &body))
		assert.Empty(t, body.Data, "nothing was created or changed after a future updated_since")
	})

	t.Run("count rejects paging, which cannot change a total", func(t *testing.T) {
		status, problem, _ := get(t, "/v1/feedback-records/count", url.Values{
			"tenant_id": {tenant}, "limit": {"1"}, "sort": {"created_at"},
		})

		require.Equal(t, http.StatusBadRequest, status)
		assert.Equal(t, []string{"limit", "sort"}, invalidParamNames(problem))
	})

	t.Run("count rejects an unknown filter", func(t *testing.T) {
		status, problem, _ := get(t, "/v1/feedback-records/count", url.Values{
			"tenant_id": {tenant}, "some_future_filter": {"x"},
		})

		require.Equal(t, http.StatusBadRequest, status)
		assert.Equal(t, []string{"some_future_filter"}, invalidParamNames(problem))
	})

	t.Run("webhooks list rejects an unknown parameter", func(t *testing.T) {
		status, problem, _ := get(t, "/v1/webhooks", url.Values{"tenant_id": {tenant}, "enabledd": {"true"}})

		require.Equal(t, http.StatusBadRequest, status)
		assert.Equal(t, []string{"enabledd"}, invalidParamNames(problem))
	})
}
