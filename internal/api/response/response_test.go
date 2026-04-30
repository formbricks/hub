package response

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
)

func TestRespondErrorProblemType(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantType   string
	}{
		{
			name:       "method not allowed",
			statusCode: http.StatusMethodNotAllowed,
			wantType:   ProblemTypeMethodNotAllowed,
		},
		{
			name:       "unlisted client error falls back to client error",
			statusCode: http.StatusTooManyRequests,
			wantType:   ProblemTypeClientError,
		},
		{
			name:       "unlisted server error falls back to internal server error",
			statusCode: http.StatusBadGateway,
			wantType:   ProblemTypeInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			RespondError(rec, tt.statusCode, http.StatusText(tt.statusCode), "test detail")

			assert.Equal(t, tt.statusCode, rec.Code)

			var problem ProblemDetails

			err := json.Unmarshal(rec.Body.Bytes(), &problem)
			require.NoError(t, err)
			assert.Equal(t, tt.wantType, problem.Type)
			assert.Equal(t, tt.statusCode, problem.Status)
		})
	}
}

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

	t.Run("invalid field type returns enum details", func(t *testing.T) {
		var req struct {
			FieldType models.FieldType `json:"field_type"`
		}

		err := json.Unmarshal([]byte(`{"field_type":"textt"}`), &req)
		require.Error(t, err)

		detail := JSONDecodeErrorDetail(err)
		assert.Contains(t, detail, "field_type")
		assert.Contains(t, detail, "text")
		assert.Contains(t, detail, "date")

		details := JSONDecodeErrorDetails(err)
		require.Len(t, details, 1)
		assert.Equal(t, "field_type", details[0].Location)
		assert.Equal(t, "textt", details[0].Value)
		assert.Contains(t, details[0].Message, "rating")
	})
}
