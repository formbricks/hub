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

// TestDecodeEnumSliceQueryParams covers the repeatable enum filters. The mechanic under test is
// that go-playground/form resolves a custom type func from the slice field's own type and hands it
// every repetition at once — without that, each element would decode through the plain-string
// branch and skip validation entirely.
func TestDecodeEnumSliceQueryParams(t *testing.T) {
	type enumFilters struct {
		FieldType []models.FieldType      `form:"field_type"`
		Sentiment []models.SentimentValue `form:"sentiment"`
		Emotions  []models.EmotionValue   `form:"emotions"`
	}

	t.Run("repeated values decode in order", func(t *testing.T) {
		var filters enumFilters

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/v1/x?sentiment=negative&sentiment=very_negative&emotions=anger&field_type=text&field_type=rating", http.NoBody)

		require.NoError(t, ValidateAndDecodeQueryParams(req, &filters))
		assert.Equal(t, []models.SentimentValue{models.SentimentNegative, models.SentimentVeryNegative}, filters.Sentiment)
		assert.Equal(t, []models.EmotionValue{models.EmotionAnger}, filters.Emotions)
		assert.Equal(t, []models.FieldType{models.FieldTypeText, models.FieldTypeRating}, filters.FieldType)
	})

	t.Run("repeats collapse", func(t *testing.T) {
		var filters enumFilters

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/v1/x?sentiment=positive&sentiment=positive", http.NoBody)

		require.NoError(t, ValidateAndDecodeQueryParams(req, &filters))
		assert.Equal(t, []models.SentimentValue{models.SentimentPositive}, filters.Sentiment)
	})

	// ?field_type= has always meant "no field_type filter" — the single-value decoder maps an
	// empty value to a nil pointer. Parsing the slice here (rather than with a `dive` validate
	// tag, which would reject "") keeps that true.
	t.Run("empty value is not a filter", func(t *testing.T) {
		var filters enumFilters

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/x?field_type=&sentiment=", http.NoBody)

		require.NoError(t, ValidateAndDecodeQueryParams(req, &filters))
		assert.Nil(t, filters.FieldType)
		assert.Nil(t, filters.Sentiment)
	})

	t.Run("unknown label is a 400 naming the bare parameter", func(t *testing.T) {
		for _, testCase := range []struct{ query, param, wantIn string }{
			{query: "sentiment=hostile", param: "sentiment", wantIn: "very_negative"},
			{query: "emotions=ennui", param: "emotions", wantIn: "disgust"},
			{query: "field_type=textt", param: "field_type", wantIn: "categorical"},
		} {
			t.Run(testCase.param, func(t *testing.T) {
				var filters enumFilters

				req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/x?"+testCase.query, http.NoBody)

				err := ValidateAndDecodeQueryParams(req, &filters)
				require.ErrorIs(t, err, ErrQueryDecodeFailed)

				var queryErr *QueryDecodeError
				require.ErrorAs(t, err, &queryErr)

				params := queryErr.InvalidParams()
				require.Len(t, params, 1)
				assert.Equal(t, testCase.param, params[0].Name, "the reason must name the parameter, not an index into it")
				assert.Contains(t, params[0].Reason, testCase.wantIn)
			})
		}
	})
}
