package repository

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/models"
)

// DeleteByUser is tested by integration tests in tests/integration_test.go:
//   - TestFeedbackRecordsRepository_DeleteByUser exercises the repository directly and asserts
//     the optional tenant filter and tenant-grouped return values.
//   - TestDeleteFeedbackRecordsByUser exercises the full stack (handler, service, repo) including
//     tenant-scoped deletion, GDPR user_id erasure across tenants, and response shape.
func TestFeedbackRecordsRepository_Package(_ *testing.T) {
	// No DB in unit tests; DeleteByUser coverage is in tests/.
}

// TestBuildUpdateQuery_ClearsStaleEnrichmentOnContentChange locks the eager-clear trigger scope in
// buildUpdateQuery, which must MIRROR each enrichment provider's `triggers` (internal/service):
// sentiment/emotions are invalidated by a value_text change alone, translation by value_text OR
// language. A non-content update (metadata, user_id) must clear nothing. Asserting on the emitted
// SQL keeps this a fast, DB-free guard against the two sides drifting apart.
func TestBuildUpdateQuery_ClearsStaleEnrichmentOnContentChange(t *testing.T) {
	text := "updated text"
	lang := "de-DE"
	user := "user-1"
	valueID := "opt_a"
	meta := json.RawMessage(`{"k":"v"}`)

	// Enrichment output columns, grouped by what invalidates them.
	translationCols := []string{"value_text_translated", "translation_lang_key"}
	// emotions_classified_at must clear with emotions: it is the completion marker, so leaving it
	// behind would keep an edited record counting as classified. It must equally NOT clear on a
	// language-only edit, which the negative cases below cover.
	textOnlyCols := []string{"sentiment", "sentiment_score", "emotions", "emotions_classified_at"}
	allCols := append(append([]string{}, translationCols...), textOnlyCols...)

	cases := []struct {
		name  string
		req   *models.UpdateFeedbackRecordRequest
		clear []string // columns whose stale-clear CASE must be emitted
	}{
		{
			"value_text change clears translation and sentiment/emotions",
			&models.UpdateFeedbackRecordRequest{ValueText: &text},
			allCols,
		},
		{
			"language change clears only translation",
			&models.UpdateFeedbackRecordRequest{Language: &lang},
			translationCols,
		},
		{
			"value_text and language change clears everything",
			&models.UpdateFeedbackRecordRequest{ValueText: &text, Language: &lang},
			allCols,
		},
		{"metadata-only change clears nothing", &models.UpdateFeedbackRecordRequest{Metadata: meta}, nil},
		{"user_id-only change clears nothing", &models.UpdateFeedbackRecordRequest{UserID: &user}, nil},
		{
			"value_id-only change clears nothing",
			&models.UpdateFeedbackRecordRequest{ValueID: &valueID},
			nil,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			query, _, hasUpdates := buildUpdateQuery(testCase.req, uuid.New(), time.Now())
			if !hasUpdates {
				t.Fatal("buildUpdateQuery hasUpdates = false, want true")
			}

			wantClear := make(map[string]bool, len(testCase.clear))
			for _, col := range testCase.clear {
				wantClear[col] = true
			}

			for _, col := range allCols {
				if got := clearsColumn(query, col); got != wantClear[col] {
					t.Fatalf("clears %s = %v, want %v\nquery: %s", col, got, wantClear[col], query)
				}
			}
		})
	}
}

// clearsColumn reports whether the query nulls col via the eager-clear CASE emitted by
// clearColumnWhen. The " = CASE WHEN" suffix makes "sentiment" not match "sentiment_score".
func clearsColumn(query, col string) bool {
	return strings.Contains(query, col+" = CASE WHEN")
}

// buildCountQuery constructs `SELECT COUNT(*) FROM feedback_records` with an optional WHERE clause
// derived from the same filter predicates used by List. Test the query string construction and arg
// count to lock the SQL generation without a database.
func TestBuildCountQuery(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	// A listing with no tenant would span every tenant. The HTTP layer marks tenant_id required,
	// but this builder is also reachable from workers and tests that construct filters directly in
	// Go, so it fails closed rather than emitting a tenant-free WHERE.
	t.Run("no filters is rejected", func(t *testing.T) {
		if _, _, err := buildCountQuery(&models.ListFeedbackRecordsFilters{}); err == nil {
			t.Fatal("buildCountQuery() error = nil, want a missing-tenant error")
		}
	})

	t.Run("blank tenant_id is rejected", func(t *testing.T) {
		blank := "   "
		if _, _, err := buildCountQuery(&models.ListFeedbackRecordsFilters{TenantID: &blank}); err == nil {
			t.Fatal("buildCountQuery() error = nil, want a missing-tenant error")
		}
	})

	t.Run("tenant_id only", func(t *testing.T) {
		tenantID := "org-123"

		query, args, err := buildCountQuery(&models.ListFeedbackRecordsFilters{TenantID: &tenantID})
		if err != nil {
			t.Fatalf("buildCountQuery() error = %v, want nil", err)
		}

		if !strings.Contains(query, "WHERE tenant_id = $1") {
			t.Fatalf("query = %q, want WHERE tenant_id = $1", query)
		}

		if len(args) != 1 || args[0] != "org-123" {
			t.Fatalf("args = %v, want [org-123]", args)
		}
	})

	// The pre-existing filters keep their placeholder positions when each carries a single value.
	// That is the backward-compatibility contract of converting them to multi-value: same rows,
	// same bind layout, just `= ANY` over a one-element array instead of `=`.
	t.Run("legacy single-value filters keep their placeholder layout", func(t *testing.T) {
		tenantID := "org-123"

		query, args, err := buildCountQuery(&models.ListFeedbackRecordsFilters{
			TenantID:     &tenantID,
			SourceType:   []string{"formbricks"},
			FieldID:      []string{"field-1"},
			UserID:       []string{"user-1"},
			SubmissionID: []string{"sub-1"},
			SourceID:     []string{"src-1"},
			FieldGroupID: []string{"fg-1"},
			FieldType:    []models.FieldType{models.FieldTypeText},
			Since:        &now,
			Until:        &now,
		})
		if err != nil {
			t.Fatalf("buildCountQuery() error = %v, want nil", err)
		}

		// Must start with base SELECT.
		if !strings.HasPrefix(query, "SELECT COUNT(*) FROM feedback_records WHERE ") {
			t.Fatalf("query = %q, want SELECT COUNT(*) prefix with WHERE", query)
		}

		// Must contain every expected condition (order doesn't matter within AND).
		wantConditions := []string{
			"tenant_id = $1",
			"submission_id = ANY($2)",
			"source_type = ANY($3)",
			"source_id = ANY($4)",
			"field_id = ANY($5)",
			"field_group_id = ANY($6)",
			"field_type = ANY($7::text[]::field_type_enum[])",
			"user_id = ANY($8)",
			"collected_at >= $9",
			"collected_at <= $10",
		}

		for _, cond := range wantConditions {
			if !strings.Contains(query, cond) {
				t.Fatalf("query missing condition %q\nquery: %s", cond, query)
			}
		}

		if len(args) != 10 {
			t.Fatalf("args count = %d, want 10; args = %v", len(args), args)
		}

		if args[0] != "org-123" {
			t.Fatalf("args[0] = %v, want org-123", args[0])
		}
	})
}

// TestBuildUpdateQuery_ValueID verifies value_id is a plain assignable column: an
// update carrying it emits a direct "value_id = $N" SET clause (not an eager-clear CASE),
// since it is caller-supplied data rather than a derived enrichment.
func TestBuildUpdateQuery_ValueID(t *testing.T) {
	valueID := "opt_very_satisfied"
	req := &models.UpdateFeedbackRecordRequest{ValueID: &valueID}

	query, args, hasUpdates := buildUpdateQuery(req, uuid.New(), time.Now())
	if !hasUpdates {
		t.Fatal("buildUpdateQuery hasUpdates = false, want true")
	}

	if !strings.Contains(query, "value_id = $1") {
		t.Fatalf("query missing direct value_id assignment\nquery: %s", query)
	}

	if clearsColumn(query, "value_id") {
		t.Fatalf("value_id must not be an eager-clear column\nquery: %s", query)
	}

	if len(args) == 0 || args[0] != valueID {
		t.Fatalf("args = %v, want first arg %q", args, valueID)
	}
}

// TestBuildFilterConditions_PlaceholdersMatchArgs locks that every generated $N placeholder maps to
// its argument's 1-based position for any combination of filters. The placeholder is derived from
// len(args)+1 at each append precisely so the order of filters can't desync it — this guards
// against a regression to a manual counter (a trailing filter that forgot to advance it would bind
// the wrong value). Every filter is set here, so the conditions appear in the function's fixed
// order and each column's placeholder must equal its position.
func TestBuildFilterConditions_PlaceholdersMatchArgs(t *testing.T) {
	tenant := "t1"
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	createdSince := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	createdUntil := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	valueDateMin := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	valueDateMax := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	numberMin, numberMax := 1.5, 9.5
	scoreMin, scoreMax := -0.5, 0.5
	hasSentiment, hasEmotions, hasTranslation := true, false, true

	where, args, err := buildFilterConditions(&models.ListFeedbackRecordsFilters{
		TenantID:     &tenant,
		SubmissionID: []string{"s1"},
		SourceType:   []string{"survey", "review"},
		SourceID:     []string{"src1"},
		FieldID:      []string{"q1"},
		FieldGroupID: []string{"g1"},
		FieldType:    []models.FieldType{models.FieldTypeCategorical, models.FieldTypeText},
		ValueID:      []string{"opt_a"},
		UserID:       []string{"u1"},
		SourceName:   []string{"After match"},
		Language:     []string{"en", "ar"},

		Since: &since, Until: &until,
		CreatedSince: &createdSince, CreatedUntil: &createdUntil,
		ValueDateMin: &valueDateMin, ValueDateMax: &valueDateMax,
		ValueNumberMin: &numberMin, ValueNumberMax: &numberMax,

		Sentiment:         []models.SentimentValue{models.SentimentNegative, models.SentimentVeryNegative},
		SentimentScoreMin: &scoreMin, SentimentScoreMax: &scoreMax,
		Emotions:       []models.EmotionValue{models.EmotionAnger},
		HasSentiment:   &hasSentiment,
		HasEmotions:    &hasEmotions,
		HasTranslation: &hasTranslation,
	})
	if err != nil {
		t.Fatalf("buildFilterConditions() error = %v, want nil", err)
	}

	expected := []struct {
		clause string
		value  any
	}{
		{"tenant_id = $1", tenant},
		{"submission_id = ANY($2)", []string{"s1"}},
		{"source_type = ANY($3)", []string{"survey", "review"}},
		{"source_id = ANY($4)", []string{"src1"}},
		{"field_id = ANY($5)", []string{"q1"}},
		{"field_group_id = ANY($6)", []string{"g1"}},
		{"field_type = ANY($7::text[]::field_type_enum[])", []string{"categorical", "text"}},
		{"value_id = ANY($8)", []string{"opt_a"}},
		{"user_id = ANY($9)", []string{"u1"}},
		{"source_name = ANY($10)", []string{"After match"}},
		{"language = ANY($11)", []string{"en", "ar"}},
		{"collected_at >= $12", since},
		{"collected_at <= $13", until},
		{"created_at >= $14", createdSince},
		{"created_at <= $15", createdUntil},
		{"value_date >= $16", valueDateMin},
		{"value_date <= $17", valueDateMax},
		{"value_number >= $18", numberMin},
		{"value_number <= $19", numberMax},
		{"sentiment = ANY($20)", []string{"negative", "very_negative"}},
		{"sentiment_score >= $21", scoreMin},
		{"sentiment_score <= $22", scoreMax},
		{"emotions && $23", []string{"anger"}},
	}

	if len(args) != len(expected) {
		t.Fatalf("args len = %d, want %d\nwhere: %s", len(args), len(expected), where)
	}

	for i, exp := range expected {
		if !strings.Contains(where, exp.clause) {
			t.Fatalf("where clause missing %q\ngot: %s", exp.clause, where)
		}

		// DeepEqual, not !=: a multi-value filter binds a []string, and comparing two interfaces
		// holding an uncomparable dynamic type panics at runtime rather than failing.
		if !reflect.DeepEqual(args[i], exp.value) {
			t.Fatalf("args[%d] = %v, want %v (placeholder in %q must bind that arg)", i, args[i], exp.value, exp.clause)
		}
	}

	// The presence filters bind nothing, so they must not consume a placeholder.
	for _, clause := range []string{"sentiment IS NOT NULL", "emotions IS NULL", "translation_lang_key IS NOT NULL"} {
		if !strings.Contains(where, clause) {
			t.Fatalf("where clause missing %q\ngot: %s", clause, where)
		}
	}
}
