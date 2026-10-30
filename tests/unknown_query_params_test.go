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

	// Review on #155: source_name[1]=survey decoded to ["", "survey"], which also matches records whose
	// source name is empty. Only the repeated form the spec declares is accepted now.
	t.Run("list refuses an indexed filter and accepts the repeated form", func(t *testing.T) {
		status, problem, _ := get(t, "/v1/feedback-records", url.Values{
			"tenant_id": {tenant}, "source_name[1]": {"survey"},
		})

		require.Equal(t, http.StatusBadRequest, status)
		require.Len(t, problem.InvalidParams, 1)
		assert.Equal(t, "source_name[1]", problem.InvalidParams[0].Name)
		assert.Contains(t, problem.InvalidParams[0].Reason, "send source_name=value")

		status, _, _ = get(t, "/v1/feedback-records", url.Values{
			"tenant_id": {tenant}, "source_name": {"survey", "other"},
		})
		assert.Equal(t, http.StatusOK, status)
	})

	t.Run("list refuses a repeated single-valued parameter", func(t *testing.T) {
		status, problem, _ := get(t, "/v1/feedback-records", url.Values{"tenant_id": {tenant, "other-tenant"}})

		require.Equal(t, http.StatusBadRequest, status)
		assert.Equal(t, []string{"tenant_id"}, invalidParamNames(problem))
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

// The decoder reads an indexed key only into a slice field, so tenant_id[0] used to be dropped and
// leave the tenant unset: DELETE ?user_id=u&tenant_id[0]=A then deleted u's records in every tenant,
// as the documented no-tenant form does. So did tenant_id=A; (see below). Both must be refused,
// with nothing deleted anywhere.
func TestDeleteByUserRefusesIndexedTenant(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{Timeout: 30 * time.Second}
	userID := "indexed-tenant-user-" + uuid.NewString()
	tenants := []string{"indexed-tenant-a-" + uuid.NewString(), "indexed-tenant-b-" + uuid.NewString()}

	send := func(t *testing.T, method, target, body string) (int, []byte) {
		t.Helper()

		req, err := http.NewRequestWithContext(context.Background(), method, server.URL+target, bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)

		defer func() { require.NoError(t, resp.Body.Close()) }()

		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		return resp.StatusCode, raw
	}

	for _, tenant := range tenants {
		status, _ := send(t, http.MethodPost, "/v1/feedback-records", fmt.Sprintf(
			`{"source_type":"survey","field_id":"q1","field_type":"text","value_text":"x","submission_id":%q,"tenant_id":%q,"user_id":%q}`,
			uuid.NewString(), tenant, userID))
		require.Equal(t, http.StatusCreated, status)
	}

	// Raw query: url.Values would percent-encode the brackets, which the server decodes the same way,
	// but spelling them out keeps the request identical to what a client sends.
	status, raw := send(t, http.MethodDelete,
		"/v1/feedback-records?user_id="+url.QueryEscape(userID)+"&tenant_id[0]="+url.QueryEscape(tenants[0]), "")
	require.Equal(t, http.StatusBadRequest, status, string(raw))

	var problem response.ProblemDetails
	require.NoError(t, json.Unmarshal(raw, &problem))
	require.Len(t, problem.InvalidParams, 1)
	assert.Equal(t, "tenant_id[0]", problem.InvalidParams[0].Name)

	// url.ParseQuery drops a pair it cannot read, so a trailing ";" used to lose the tenant the same
	// way.
	status, raw = send(t, http.MethodDelete,
		"/v1/feedback-records?user_id="+url.QueryEscape(userID)+"&tenant_id="+url.QueryEscape(tenants[0])+";", "")
	require.Equal(t, http.StatusBadRequest, status, string(raw))
	require.NoError(t, json.Unmarshal(raw, &problem))
	require.Len(t, problem.InvalidParams, 1)
	assert.Equal(t, "query", problem.InvalidParams[0].Name)

	for _, tenant := range tenants {
		status, raw := send(t, http.MethodGet, "/v1/feedback-records/count?tenant_id="+url.QueryEscape(tenant)+
			"&user_id="+url.QueryEscape(userID), "")
		require.Equal(t, http.StatusOK, status)

		var count models.CountFeedbackRecordsResponse
		require.NoError(t, json.Unmarshal(raw, &count))
		assert.Equal(t, int64(1), count.Count, "tenant %s lost its record", tenant)
	}
}

// An empty value is documented as no constraint, but the decoder read ?source_name= as a filter on
// "", so it returned only records with a blank source name, and source_name=&source_name=X also
// matched blank ones (review on #155).
func TestEmptyFilterValueIsNoConstraint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{Timeout: 30 * time.Second}
	tenant := "empty-value-" + uuid.NewString()

	for _, sourceName := range []string{"Alpha", "Beta", ""} {
		body := fmt.Sprintf(`{"source_type":"survey","field_id":"q1","field_type":"text","value_text":"x",`+
			`"submission_id":%q,"tenant_id":%q,"source_name":%q}`, uuid.NewString(), tenant, sourceName)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
			server.URL+"/v1/feedback-records", bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	}

	count := func(t *testing.T, rawQuery string) int64 {
		t.Helper()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			server.URL+"/v1/feedback-records/count?tenant_id="+url.QueryEscape(tenant)+"&"+rawQuery, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)

		defer func() { require.NoError(t, resp.Body.Close()) }()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var body models.CountFeedbackRecordsResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

		return body.Count
	}

	assert.Equal(t, int64(3), count(t, "source_name="), "an empty value must not filter")
	assert.Equal(t, int64(3), count(t, "source_type="), "an empty value must not filter")
	assert.Equal(t, int64(1), count(t, "source_name=&source_name=Alpha"), "the empty value must not add blank names")
}
