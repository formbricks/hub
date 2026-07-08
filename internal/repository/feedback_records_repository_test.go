package repository

import (
	"encoding/json"
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
	textOnlyCols := []string{"sentiment", "sentiment_score", "emotions"}
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

	t.Run("no filters", func(t *testing.T) {
		query, args := buildCountQuery(&models.ListFeedbackRecordsFilters{})
		if query != "SELECT COUNT(*) FROM feedback_records" {
			t.Fatalf("query = %q, want SELECT COUNT(*) FROM feedback_records", query)
		}

		if len(args) != 0 {
			t.Fatalf("args = %v, want empty", args)
		}
	})

	t.Run("tenant_id only", func(t *testing.T) {
		tenantID := "org-123"

		query, args := buildCountQuery(&models.ListFeedbackRecordsFilters{TenantID: &tenantID})
		if !strings.Contains(query, "WHERE tenant_id = $1") {
			t.Fatalf("query = %q, want WHERE tenant_id = $1", query)
		}

		if len(args) != 1 || args[0] != "org-123" {
			t.Fatalf("args = %v, want [org-123]", args)
		}
	})

	t.Run("all filters combined", func(t *testing.T) {
		tenantID := "org-123"
		sourceType := "formbricks"
		fieldID := "field-1"
		userID := "user-1"
		submissionID := "sub-1"
		sourceID := "src-1"
		fieldGroupID := "fg-1"
		fieldType := models.FieldTypeText

		query, args := buildCountQuery(&models.ListFeedbackRecordsFilters{
			TenantID:     &tenantID,
			SourceType:   &sourceType,
			FieldID:      &fieldID,
			UserID:       &userID,
			SubmissionID: &submissionID,
			SourceID:     &sourceID,
			FieldGroupID: &fieldGroupID,
			FieldType:    &fieldType,
			Since:        &now,
			Until:        &now,
		})

		// Must start with base SELECT.
		if !strings.HasPrefix(query, "SELECT COUNT(*) FROM feedback_records WHERE ") {
			t.Fatalf("query = %q, want SELECT COUNT(*) prefix with WHERE", query)
		}

		// Must contain every expected condition (order doesn't matter within AND).
		wantConditions := []string{
			"tenant_id = $1",
			"submission_id = $2",
			"source_type = $3",
			"source_id = $4",
			"field_id = $5",
			"field_group_id = $6",
			"field_type = $7",
			"user_id = $8",
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
	submission := "s1"
	sourceType := "survey"
	sourceID := "src1"
	fieldID := "q1"
	fieldGroupID := "g1"
	fieldType := models.FieldTypeCategorical
	valueID := "opt_a"
	userID := "u1"
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	where, args := buildFilterConditions(&models.ListFeedbackRecordsFilters{
		TenantID: &tenant, SubmissionID: &submission, SourceType: &sourceType,
		SourceID: &sourceID, FieldID: &fieldID, FieldGroupID: &fieldGroupID,
		FieldType: &fieldType, ValueID: &valueID, UserID: &userID,
		Since: &since, Until: &until,
	})

	expected := []struct {
		clause string
		value  any
	}{
		{"tenant_id = $1", tenant},
		{"submission_id = $2", submission},
		{"source_type = $3", sourceType},
		{"source_id = $4", sourceID},
		{"field_id = $5", fieldID},
		{"field_group_id = $6", fieldGroupID},
		{"field_type = $7", fieldType},
		{"value_id = $8", valueID},
		{"user_id = $9", userID},
		{"collected_at >= $10", since},
		{"collected_at <= $11", until},
	}

	if len(args) != len(expected) {
		t.Fatalf("args len = %d, want %d\nwhere: %s", len(args), len(expected), where)
	}

	for i, exp := range expected {
		if !strings.Contains(where, exp.clause) {
			t.Fatalf("where clause missing %q\ngot: %s", exp.clause, where)
		}

		if args[i] != exp.value {
			t.Fatalf("args[%d] = %v, want %v (placeholder in %q must bind that arg)", i, args[i], exp.value, exp.clause)
		}
	}
}
