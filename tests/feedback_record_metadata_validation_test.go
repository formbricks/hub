package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ENG-2745: metadata that Postgres cannot store must come back as a 400 naming `metadata`, never as
// an unmapped 500. Two mechanisms are under test, so this deliberately drives the full HTTP stack
// against the real database: the storable_json request validator (NUL bytes, unpaired surrogates)
// and the repository's SQLSTATE 22003 mapping (numbers outside numeric range, which no byte scan
// can see — the value is fifteen bytes of perfectly valid JSON).
func TestFeedbackRecordMetadataUnstorableInputIsA400(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Unique per run: the accepted-metadata subtest inserts a real row, and the identity triple
	// (tenant_id, submission_id, field_id) would 409 on the next run against the same database.
	tenantID := "metadata-validation-" + uuid.NewString()

	postRecord := func(t *testing.T, metadataJSON string) (int, string) {
		t.Helper()

		body := fmt.Sprintf(`{
			"tenant_id": "%s-%s",
			"submission_id": "s1",
			"source_type": "survey",
			"field_id": "q1",
			"field_type": "text",
			"value_text": "ok",
			"metadata": %s
		}`, tenantID, t.Name(), metadataJSON)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
			server.URL+"/v1/feedback-records", bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)

		defer func() { require.NoError(t, resp.Body.Close()) }()

		payload, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		return resp.StatusCode, string(payload)
	}

	metadataParamNames := func(t *testing.T, payload string) []string {
		t.Helper()

		var problem struct {
			InvalidParams []struct {
				Name string `json:"name"`
			} `json:"invalid_params"`
		}
		require.NoError(t, json.Unmarshal([]byte(payload), &problem))

		names := make([]string, 0, len(problem.InvalidParams))
		for _, param := range problem.InvalidParams {
			names = append(names, param.Name)
		}

		return names
	}

	t.Run("null byte is rejected by the request validator", func(t *testing.T) {
		status, payload := postRecord(t, `{"note":"bad\u0000value"}`)

		assert.Equal(t, http.StatusBadRequest, status, payload)
		assert.Contains(t, metadataParamNames(t, payload), "metadata")
	})

	t.Run("unpaired surrogate is rejected by the request validator", func(t *testing.T) {
		status, payload := postRecord(t, `{"note":"bad\ud83dvalue"}`)

		assert.Equal(t, http.StatusBadRequest, status, payload)
		assert.Contains(t, metadataParamNames(t, payload), "metadata")
	})

	t.Run("numeric overflow is rejected by the repository mapping", func(t *testing.T) {
		// Valid JSON, storable characters — only the parsed value overflows Postgres numeric, so
		// this can only fail closed if the 22003 mapping is in place.
		status, payload := postRecord(t, `{"n":1e1000000}`)

		assert.Equal(t, http.StatusBadRequest, status, payload)
		assert.Contains(t, metadataParamNames(t, payload), "metadata")
		assert.Contains(t, payload, "outside the storable range")
	})

	t.Run("ordinary metadata still stores", func(t *testing.T) {
		status, payload := postRecord(t, `{"source":"link","big":9007199254740993}`)

		assert.Equal(t, http.StatusCreated, status, payload)
	})
}
