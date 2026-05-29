package validation

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
)

func TestValidateStructUsesAPITagNames(t *testing.T) {
	t.Run("json tag", func(t *testing.T) {
		var req struct {
			FieldID string `json:"field_id" validate:"required"`
		}

		err := ValidateStruct(req)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrValidationFailed)
		assert.Equal(t, "validation failed: field_id is required", err.Error())

		var validationErrors validator.ValidationErrors
		require.ErrorAs(t, err, &validationErrors)
		require.Len(t, validationErrors, 1)
		assert.Equal(t, "field_id", validationErrors[0].Field())
	})

	t.Run("form tag", func(t *testing.T) {
		var filters struct {
			TenantID *string `form:"tenant_id" validate:"required"`
		}

		err := ValidateStruct(filters)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrValidationFailed)
		assert.Equal(t, "validation failed: tenant_id is required", err.Error())

		var validationErrors validator.ValidationErrors
		require.ErrorAs(t, err, &validationErrors)
		require.Len(t, validationErrors, 1)
		assert.Equal(t, "tenant_id", validationErrors[0].Field())
	})
}

func TestValidateStructPreservesValidationDetails(t *testing.T) {
	valueText := "contains\x00null"
	req := struct {
		ValueText *string `json:"value_text,omitempty" validate:"omitempty,no_null_bytes"`
	}{
		ValueText: &valueText,
	}

	err := ValidateStruct(req)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Equal(t, "validation failed: value_text must not contain NULL bytes", err.Error())

	var validationErrors validator.ValidationErrors
	require.ErrorAs(t, err, &validationErrors)
	require.Len(t, validationErrors, 1)
	assert.Equal(t, "value_text", validationErrors[0].Field())
	assert.Equal(t, "no_null_bytes", validationErrors[0].Tag())
	assert.Equal(t, valueText, validationErrors[0].Value())
}

func TestValidateAndDecodeQueryParamsReturnsInvalidParams(t *testing.T) {
	t.Run("standard decoder errors", func(t *testing.T) {
		var filters struct {
			Enabled *bool      `form:"enabled"`
			Limit   int        `form:"limit"`
			Score   float64    `form:"score"`
			Since   *time.Time `form:"since"`
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/v1/x?enabled=maybe&limit=abc&score=xyz&since=not-a-date", http.NoBody)

		err := ValidateAndDecodeQueryParams(req, &filters)
		require.ErrorIs(t, err, ErrQueryDecodeFailed)

		var queryErr *QueryDecodeError
		require.ErrorAs(t, err, &queryErr)

		params := map[string]string{}
		for _, param := range queryErr.InvalidParams() {
			params[param.Name] = param.Reason
		}

		assert.Equal(t, "must be a valid boolean", params["enabled"])
		assert.Equal(t, "must be a valid integer", params["limit"])
		assert.Equal(t, "must be a valid number", params["score"])
		assert.Equal(t, "must be in RFC3339 (ISO 8601) format", params["since"])
	})

	t.Run("custom field type errors", func(t *testing.T) {
		var filters struct {
			FieldType *models.FieldType `form:"field_type"`
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/x?field_type=textt", http.NoBody)

		err := ValidateAndDecodeQueryParams(req, &filters)
		require.ErrorIs(t, err, ErrQueryDecodeFailed)

		var queryErr *QueryDecodeError
		require.ErrorAs(t, err, &queryErr)

		params := queryErr.InvalidParams()
		require.Len(t, params, 1)
		assert.Equal(t, "field_type", params[0].Name)
		assert.Contains(t, params[0].Reason, "text")
		assert.Contains(t, params[0].Reason, "date")
	})
}
