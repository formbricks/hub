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
