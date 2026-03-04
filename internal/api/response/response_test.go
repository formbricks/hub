package response

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONDecodeErrorDetail(t *testing.T) {
	t.Run("nil returns generic message", func(t *testing.T) {
		assert.Equal(t, "Invalid request body", JSONDecodeErrorDetail(nil))
	})

	t.Run("syntax error returns descriptive message", func(t *testing.T) {
		var req struct{}

		err := json.Unmarshal([]byte(`{invalid}`), &req)
		require.Error(t, err)
		detail := JSONDecodeErrorDetail(err)
		assert.Contains(t, detail, "Invalid JSON")
		assert.Contains(t, detail, "invalid character")
	})

	t.Run("type error returns field and type", func(t *testing.T) {
		var req struct {
			TenantID string `json:"tenant_id"`
		}

		err := json.Unmarshal([]byte(`{"tenant_id": 123}`), &req)
		require.Error(t, err)
		detail := JSONDecodeErrorDetail(err)
		assert.Contains(t, detail, "tenant_id")
		assert.Contains(t, detail, "string")
	})

	t.Run("unknown field returns message", func(t *testing.T) {
		var req struct {
			Query string `json:"query"`
		}

		dec := json.NewDecoder(bytes.NewReader([]byte(`{"query":"x","tenantId":"y"}`)))
		dec.DisallowUnknownFields()
		err := dec.Decode(&req)
		require.Error(t, err)
		detail := JSONDecodeErrorDetail(err)
		assert.Contains(t, detail, "unknown field")
		assert.Contains(t, detail, "tenantId")
	})
}
