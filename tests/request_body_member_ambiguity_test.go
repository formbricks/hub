package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/models"
)

// ENG-3658: a gateway authorizes the exact `tenant_id` member of a create body and forwards the bytes
// unchanged. encoding/json would read `TENANT_ID` as the same field, so a body carrying both must be
// refused here — through the real router, and with nothing written under either tenant.
func TestCreateFeedbackRecordRefusesAmbiguousTenant(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{Timeout: 30 * time.Second}
	authorizedTenant := "ambiguity-authorized-" + uuid.NewString()
	otherTenant := "ambiguity-other-" + uuid.NewString()

	post := func(t *testing.T, body string) (int, response.ProblemDetails) {
		t.Helper()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
			server.URL+"/v1/feedback-records", bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)

		defer func() { require.NoError(t, resp.Body.Close()) }()

		var problem response.ProblemDetails
		if resp.StatusCode >= http.StatusBadRequest {
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&problem))
		}

		return resp.StatusCode, problem
	}

	count := func(t *testing.T, tenantID string) int64 {
		t.Helper()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			server.URL+"/v1/feedback-records/count?tenant_id="+tenantID, http.NoBody)
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

	record := func(members string) string {
		return fmt.Sprintf(`{"source_type":"survey","field_id":"q1","field_type":"text","value_text":"x",`+
			`"submission_id":%q,%s}`, uuid.NewString(), members)
	}

	refused := map[string]struct{ members, invalidParam string }{
		"case variant after": {
			fmt.Sprintf(`"tenant_id":%q,"TENANT_ID":%q`, authorizedTenant, otherTenant), "TENANT_ID",
		},
		"case variant before": {
			fmt.Sprintf(`"Tenant_Id":%q,"tenant_id":%q`, otherTenant, authorizedTenant), "Tenant_Id",
		},
		"repeated member": {
			fmt.Sprintf(`"tenant_id":%q,"tenant_id":%q`, authorizedTenant, otherTenant), "tenant_id",
		},
	}

	for name, refusedCase := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			status, problem := post(t, record(refusedCase.members))

			assert.Equal(t, http.StatusBadRequest, status)
			require.Len(t, problem.InvalidParams, 1)
			assert.Equal(t, refusedCase.invalidParam, problem.InvalidParams[0].Name)
		})
	}

	t.Run("refuses trailing data", func(t *testing.T) {
		status, problem := post(t, record(fmt.Sprintf(`"tenant_id":%q`, authorizedTenant))+
			record(fmt.Sprintf(`"tenant_id":%q`, otherTenant)))

		assert.Equal(t, http.StatusBadRequest, status)
		assert.True(t, strings.HasPrefix(problem.Detail, "Invalid JSON: "), problem.Detail)
		assert.Contains(t, problem.Detail, "after top-level value")
	})

	assert.Zero(t, count(t, otherTenant), "nothing may be written under the tenant the gateway never authorized")
	assert.Zero(t, count(t, authorizedTenant), "a refused body writes nothing at all")

	// Control: the same request with one unambiguous tenant is accepted and counted, so the zero
	// counts above are not an artifact of the count query.
	status, _ := post(t, record(fmt.Sprintf(`"tenant_id":%q`, authorizedTenant)))
	require.Equal(t, http.StatusCreated, status)
	assert.Equal(t, int64(1), count(t, authorizedTenant))
	assert.Zero(t, count(t, otherTenant))
}
