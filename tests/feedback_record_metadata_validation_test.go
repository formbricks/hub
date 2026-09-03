package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

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

	client := &http.Client{Timeout: 30 * time.Second}

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

	t.Run("ordinary metadata stores and round-trips exactly", func(t *testing.T) {
		// The spec promises "a large integer id round-trips exactly", so pin it. Decoded with
		// UseNumber deliberately: through `map[string]any` this value becomes a float64 and reads
		// back ...92, which is the regression this guards — and which assert.JSONEq would miss,
		// since it would lose the same precision on both sides of the comparison.
		status, payload := postRecord(t, `{"source":"link","big":9007199254740993}`)
		require.Equal(t, http.StatusCreated, status, payload)

		var record struct {
			Metadata json.RawMessage `json:"metadata"`
		}
		require.NoError(t, json.Unmarshal([]byte(payload), &record))

		decoder := json.NewDecoder(bytes.NewReader(record.Metadata))
		decoder.UseNumber()

		var stored map[string]any
		require.NoError(t, decoder.Decode(&stored))

		assert.Equal(t, "link", stored["source"])
		assert.Equal(t, json.Number("9007199254740993"), stored["big"])
	})

	// The update path has its own validator tag and its own 22003 mapping. Without this, deleting
	// either leaves the suite green while PATCH regresses to a 500.
	t.Run("the update path rejects the same three classes", func(t *testing.T) {
		status, payload := postRecord(t, `{"source":"link"}`)
		require.Equal(t, http.StatusCreated, status, payload)

		var created struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal([]byte(payload), &created))
		require.NotEmpty(t, created.ID)

		patch := func(t *testing.T, body string) (int, string) {
			t.Helper()

			req, err := http.NewRequestWithContext(context.Background(), http.MethodPatch,
				server.URL+"/v1/feedback-records/"+created.ID, bytes.NewBufferString(body))
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

		// Baseline first: an empty PATCH succeeds, so a 400 below cannot be "any PATCH body fails".
		status, payload = patch(t, `{}`)
		require.Equal(t, http.StatusOK, status, payload)

		for _, unstorable := range []struct {
			name     string
			metadata string
		}{
			{name: "null byte", metadata: `{"note":"bad\u0000value"}`},
			{name: "unpaired surrogate", metadata: `{"note":"bad\ud83dvalue"}`},
			{name: "numeric overflow", metadata: `{"n":1e1000000}`},
		} {
			t.Run(unstorable.name, func(t *testing.T) {
				status, payload := patch(t, `{"metadata": `+unstorable.metadata+`}`)

				assert.Equal(t, http.StatusBadRequest, status, payload)
				// Naming `metadata` rules out an incidental 400 (a bad id would name `id`).
				assert.Contains(t, metadataParamNames(t, payload), "metadata")
			})
		}

		t.Run("and still accepts storable metadata", func(t *testing.T) {
			status, payload := patch(t, `{"metadata": {"source":"app"}}`)

			assert.Equal(t, http.StatusOK, status, payload)
		})
	})
}
