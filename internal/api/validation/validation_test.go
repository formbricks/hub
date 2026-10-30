package validation

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// TestValidateUpdatedAtSortIsAscendingOnly covers the struct-level rule that sort=updated_at needs
// order=asc spelled out: descending would skip records that change mid-traversal, and an omitted
// order defaults to desc. The 400 names order, the parameter the client has to change.
func TestValidateUpdatedAtSortIsAscendingOnly(t *testing.T) {
	tenant := "org-123"

	for _, order := range []models.SortOrder{"", models.SortOrderDesc} {
		t.Run("rejects order="+string(order), func(t *testing.T) {
			err := ValidateStruct(models.ListFeedbackRecordsFilters{
				TenantID: &tenant, Sort: models.SortFieldUpdatedAt, Order: order,
			})
			require.Error(t, err)

			var validationErrs validator.ValidationErrors
			require.ErrorAs(t, err, &validationErrs)
			require.Len(t, validationErrs, 1)
			assert.Equal(t, "order", FieldPath(validationErrs[0]))
			assert.Equal(t, models.UpdatedAtAscendingOnlyReason, FormatFieldError(validationErrs[0]))
		})
	}

	t.Run("accepts order=asc", func(t *testing.T) {
		require.NoError(t, ValidateStruct(models.ListFeedbackRecordsFilters{
			TenantID: &tenant, Sort: models.SortFieldUpdatedAt, Order: models.SortOrderAsc,
		}))
	})

	t.Run("leaves the other sorts' default order alone", func(t *testing.T) {
		require.NoError(t, ValidateStruct(models.ListFeedbackRecordsFilters{
			TenantID: &tenant, Sort: models.SortFieldCreatedAt,
		}))
	})

	// An unrecognised order is the oneof tag's to report; one parameter gets one reason.
	t.Run("does not double-report an invalid order", func(t *testing.T) {
		err := ValidateStruct(models.ListFeedbackRecordsFilters{
			TenantID: &tenant, Sort: models.SortFieldUpdatedAt, Order: models.SortOrder("ASC"),
		})
		require.Error(t, err)

		var validationErrs validator.ValidationErrors
		require.ErrorAs(t, err, &validationErrs)
		require.Len(t, validationErrs, 1)
		assert.Equal(t, "oneof", validationErrs[0].Tag())
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
			UpdatedSince: &late,
			UpdatedUntil: &early,
		}

		err := ValidateStruct(filters)
		require.Error(t, err)

		var validationErrs validator.ValidationErrors
		require.ErrorAs(t, err, &validationErrs)

		names := make([]string, 0, len(validationErrs))
		for _, fieldErr := range validationErrs {
			names = append(names, FieldPath(fieldErr))
		}

		assert.ElementsMatch(t, []string{"since", "created_since", "updated_since"}, names)
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
// The API now refuses the indexed form before decoding, so this decodes with the package decoder
// directly: the struct tags, not the decoder or that check, are what hold the "no unknown label"
// invariant, and they must keep holding it however the struct was populated.
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

			query, err := url.ParseQuery("tenant_id=t1&" + testCase.query)
			require.NoError(t, err)
			require.NoError(t, decoder.Decode(&filters, query))

			err = ValidateStruct(&filters)
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

		query := url.Values{"tenant_id": {"t1"}}
		for i := range 8 {
			query.Set("sentiment["+strconv.Itoa(i)+"]", "neutral")
		}

		require.NoError(t, decoder.Decode(&filters, query))
		require.Error(t, ValidateStruct(&filters))
	})
}

func TestDecodeQueryParamsRejectsUnknownParams(t *testing.T) {
	t.Run("names every unknown parameter, sorted", func(t *testing.T) {
		var filters models.ListFeedbackRecordsFilters

		// updatd_since is the misspelling that used to return every record, as if no filter had
		// been sent; zzz stands for a filter only a newer Hub knows.
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/v1/x?tenant_id=t1&zzz=1&updatd_since=2099-01-01T00:00:00Z", http.NoBody)

		err := ValidateAndDecodeQueryParams(req, &filters)
		require.ErrorIs(t, err, ErrQueryDecodeFailed)

		var queryErr *QueryDecodeError
		require.ErrorAs(t, err, &queryErr)
		assert.Equal(t, []InvalidParam{
			{Name: "updatd_since", Reason: ReasonUnknownQueryParam},
			{Name: "zzz", Reason: ReasonUnknownQueryParam},
		}, queryErr.InvalidParams())
	})

	t.Run("accepts every parameter the struct declares", func(t *testing.T) {
		var filters models.ListFeedbackRecordsFilters

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/v1/x?tenant_id=t1&updated_since=2026-01-01T00:00:00Z&updated_until=2026-02-01T00:00:00Z"+
				"&sort=updated_at&order=asc&limit=10&source_type=survey&source_type=chat", http.NoBody)

		require.NoError(t, ValidateAndDecodeQueryParams(req, &filters))
		require.NotNil(t, filters.UpdatedSince)
		assert.Equal(t, []string{"survey", "chat"}, filters.SourceType)
	})

	// The indexed form decodes into the same field, so it stays accepted under its base name.
	// Only the repeated form the spec declares is accepted. The decoder's indexed form turns each of
	// these into a filter other than the one sent: a gap becomes "" (["", "survey"] also matches an
	// empty source name), a repeated index keeps one value, a leading zero shifts the index, and an
	// indexed key overwrites the plain one (["b"], dropping "a").
	t.Run("refuses the indexed form of a declared parameter, naming the encoding to use", func(t *testing.T) {
		for _, rawQuery := range []string{
			"source_name[1]=survey",
			"source_name[0]=a",
			"source_name[0]=a&source_name[1]=b",
			"source_name[0]=a&source_name[2]=c",
			"source_name[0]=a&source_name[0]=b",
			"source_name[01]=a",
			"source_name=a&source_name[0]=b",
			"field_type[0]=text",
		} {
			var filters models.ListFeedbackRecordsFilters

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/x?tenant_id=t1&"+rawQuery, http.NoBody)

			var queryErr *QueryDecodeError
			require.ErrorAsf(t, ValidateAndDecodeQueryParams(req, &filters), &queryErr, "query %q", rawQuery)
			require.NotEmptyf(t, queryErr.InvalidParams(), "query %q", rawQuery)

			for _, param := range queryErr.InvalidParams() {
				name, _, _ := strings.Cut(param.Name, "[")
				assert.Equalf(t, "the indexed form is not accepted: send "+name+"=value, repeating "+name+
					" once per value where it takes several", param.Reason, "query %q", rawQuery)
			}

			assert.Emptyf(t, filters.SourceName, "query %q decoded anyway", rawQuery)
		}
	})

	t.Run("rejects the indexed form of an unknown parameter", func(t *testing.T) {
		var filters models.ListFeedbackRecordsFilters

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/v1/x?tenant_id=t1&source_typo[0]=survey", http.NoBody)

		var queryErr *QueryDecodeError
		require.ErrorAs(t, ValidateAndDecodeQueryParams(req, &filters), &queryErr)
		assert.Equal(t, []InvalidParam{{Name: "source_typo[0]", Reason: ReasonUnknownQueryParam}},
			queryErr.InvalidParams())
	})

	// The decoder reads name[<index>] only into a slice field and silently ignores every other
	// bracketed key. tenant_id[0] alone is the dangerous one: it leaves the tenant unset.
	t.Run("rejects bracketed keys the decoder would ignore", func(t *testing.T) {
		for _, key := range []string{
			"tenant_id[0]", "updated_since[0]", "sort[0]", "limit[0]",
			"source_type[]", "source_type[a]", "source_type[0][1]", "source_type[0]x", "source_type[0",
		} {
			var filters models.ListFeedbackRecordsFilters

			query := url.Values{"tenant_id": {"t1"}, key: {"2026-01-01T00:00:00Z"}}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/x?"+query.Encode(), http.NoBody)

			var queryErr *QueryDecodeError
			require.ErrorAsf(t, ValidateAndDecodeQueryParams(req, &filters), &queryErr, "key %q", key)
			require.Len(t, queryErr.InvalidParams(), 1)
			assert.Equal(t, key, queryErr.InvalidParams()[0].Name)
			assert.Contains(t, queryErr.InvalidParams()[0].Reason, "indexed form is not accepted")
		}
	})

	t.Run("bounds what it echoes back", func(t *testing.T) {
		var filters models.ListFeedbackRecordsFilters

		query := url.Values{"tenant_id": {"t1"}, strings.Repeat("k", 500): {"1"}}
		for i := range 30 {
			query.Set(fmt.Sprintf("x%02d", i), "1")
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/x?"+query.Encode(), http.NoBody)

		var queryErr *QueryDecodeError
		require.ErrorAs(t, ValidateAndDecodeQueryParams(req, &filters), &queryErr)

		params := queryErr.InvalidParams()
		require.Len(t, params, maxReportedQueryParams+1)
		assert.Equal(t, strings.Repeat("k", maxReportedQueryParamRunes)+"…", params[0].Name)
		assert.Equal(t, InvalidParam{Name: "query", Reason: "has 11 more unrecognized parameters, not listed"},
			params[maxReportedQueryParams])
	})

	t.Run("WithoutQueryParams narrows what the struct accepts", func(t *testing.T) {
		var filters models.ListFeedbackRecordsFilters

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/v1/x?tenant_id=t1&limit=10&cursor=abc", http.NoBody)

		var queryErr *QueryDecodeError
		require.ErrorAs(t, ValidateAndDecodeQueryParams(req, &filters, WithoutQueryParams("limit", "cursor")), &queryErr)
		assert.Equal(t, []InvalidParam{
			{Name: "cursor", Reason: ReasonUnknownQueryParam},
			{Name: "limit", Reason: ReasonUnknownQueryParam},
		}, queryErr.InvalidParams())
	})
}

func TestRejectUnknownQueryParams(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/v1/x?tenant_id=t1&limit=5&min_scor=0.9", http.NoBody)

	err := RejectUnknownQueryParams(req, "tenant_id", "limit", "min_score")
	require.ErrorIs(t, err, ErrQueryDecodeFailed)

	var queryErr *QueryDecodeError
	require.ErrorAs(t, err, &queryErr)
	assert.Equal(t, []InvalidParam{{Name: "min_scor", Reason: ReasonUnknownQueryParam}}, queryErr.InvalidParams())

	require.NoError(t, RejectUnknownQueryParams(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/x?tenant_id=t1&limit=5", http.NoBody),
		"tenant_id", "limit", "min_score"))

	// Query().Get reads only the exact name, so no indexed form is accepted for these handlers.
	require.ErrorAs(t, RejectUnknownQueryParams(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/x?min_score[0]=0.9", http.NoBody),
		"tenant_id", "limit", "min_score"), &queryErr)
	assert.Equal(t, []InvalidParam{{
		Name:   "min_score[0]",
		Reason: "the indexed form is not accepted: send min_score=value, repeating min_score once per value where it takes several",
	}}, queryErr.InvalidParams())
}

func TestNoReplacementChar(t *testing.T) {
	type request struct {
		TenantID *string `json:"tenant_id,omitempty" validate:"omitempty,no_replacement_char"`
		Count    int     `json:"count"               validate:"no_replacement_char"`
	}

	valid := "tenant-a ünïcödé"
	assert.NoError(t, ValidateStruct(request{TenantID: &valid}), "other non-ASCII text is fine")
	assert.NoError(t, ValidateStruct(request{}), "a nil pointer is left to omitempty")

	replaced := "tenant-a" + string(utf8.RuneError)
	err := ValidateStruct(request{TenantID: &replaced})
	require.Error(t, err)

	var validationErrors validator.ValidationErrors
	require.ErrorAs(t, err, &validationErrors)
	require.Len(t, validationErrors, 1)
	assert.Equal(t, "tenant_id", validationErrors[0].Field())
	assert.Equal(t, "must be valid UTF-8, with no unpaired UTF-16 surrogates or U+FFFD characters",
		FormatFieldError(validationErrors[0]))
}
