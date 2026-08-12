package validation

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

// TestValidateInvertedRangeFilters verifies an inverted min/max pair is a 400 naming the lower
// bound, rather than an empty result page the caller reads as "there is no such feedback".
func TestValidateInvertedRangeFilters(t *testing.T) {
	tenant := "org-123"

	t.Run("inverted score range names the lower bound", func(t *testing.T) {
		high, low := 0.9, 0.1
		filters := models.ListFeedbackRecordsFilters{
			TenantID:          &tenant,
			SentimentScoreMin: &high,
			SentimentScoreMax: &low,
		}

		err := ValidateStruct(filters)
		require.Error(t, err)

		var validationErrs validator.ValidationErrors
		require.ErrorAs(t, err, &validationErrs)
		require.Len(t, validationErrs, 1)
		assert.Equal(t, "sentiment_score_min", FieldPath(validationErrs[0]))
		assert.Equal(t, "must be less than or equal to sentiment_score_max", FormatFieldError(validationErrs[0]))
	})

	// Each pair is reported separately so a caller fixing a form sees every backwards filter at
	// once rather than one per round trip.
	t.Run("every inverted pair is reported", func(t *testing.T) {
		early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		late := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
		filters := models.ListFeedbackRecordsFilters{
			TenantID:     &tenant,
			Since:        &late,
			Until:        &early,
			CreatedSince: &late,
			CreatedUntil: &early,
		}

		err := ValidateStruct(filters)
		require.Error(t, err)

		var validationErrs validator.ValidationErrors
		require.ErrorAs(t, err, &validationErrs)

		names := make([]string, 0, len(validationErrs))
		for _, fieldErr := range validationErrs {
			names = append(names, FieldPath(fieldErr))
		}

		assert.ElementsMatch(t, []string{"since", "created_since"}, names)
	})

	// A lower bound with no counterpart is unbounded above, not inverted. Expressing this rule as
	// `ltefield` instead would fail here, because validator treats a nil counterpart as a failure
	// rather than skipping the comparison.
	t.Run("a lone bound is accepted", func(t *testing.T) {
		high := 0.9
		filters := models.ListFeedbackRecordsFilters{TenantID: &tenant, SentimentScoreMin: &high}

		assert.NoError(t, ValidateStruct(filters))
	})

	t.Run("an ordered range is accepted", func(t *testing.T) {
		low, high := 0.1, 0.9
		filters := models.ListFeedbackRecordsFilters{
			TenantID:          &tenant,
			SentimentScoreMin: &low,
			SentimentScoreMax: &high,
		}

		assert.NoError(t, ValidateStruct(filters))
	})
}

// TestDecodeEnumSliceIndexedFormIsValidated is the regression guard for a real gap: the decoder
// consults its custom type funcs only when the plain query key is present, so
// go-playground/form's indexed form takes a different branch and skips them entirely. Before the
// dive tags were added, `?field_type[0]=bogus` decoded to []FieldType{"bogus"} with no error —
// which then reached Postgres and failed the field_type_enum cast as an unmapped 500, and for
// sentiment/emotions was accepted silently.
//
// The struct tags, not the decoder, are what actually hold the "no unknown label" invariant.
func TestDecodeEnumSliceIndexedFormIsValidated(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		wantIn string
	}{
		{name: "field_type", query: "field_type[0]=bogus", wantIn: "categorical"},
		{name: "sentiment", query: "sentiment[0]=hostile", wantIn: "very_negative"},
		{name: "emotions", query: "emotions[0]=ennui", wantIn: "disgust"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var filters models.ListFeedbackRecordsFilters

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
				"/v1/x?tenant_id=t1&"+testCase.query, http.NoBody)

			err := ValidateAndDecodeQueryParams(req, &filters)
			require.Error(t, err, "the indexed form must not bypass enum validation")

			var validationErrs validator.ValidationErrors
			require.ErrorAs(t, err, &validationErrs)
			require.Len(t, validationErrs, 1)
			assert.Contains(t, FormatFieldError(validationErrs[0]), testCase.wantIn)
		})
	}

	// The same gate bounds element count on the path where the decoder's dedupe does not run.
	t.Run("indexed form is length-capped", func(t *testing.T) {
		var filters models.ListFeedbackRecordsFilters

		var query strings.Builder

		query.WriteString("/v1/x?tenant_id=t1")

		for i := range 8 {
			query.WriteString("&sentiment[" + strconv.Itoa(i) + "]=neutral")
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, query.String(), http.NoBody)
		require.Error(t, ValidateAndDecodeQueryParams(req, &filters))
	})
}
