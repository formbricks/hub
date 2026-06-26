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

// TestBuildUpdateQuery_ClearsStaleTranslationOnContentChange asserts that an update touching
// value_text or language also nulls value_text_translated / translation_lang_key — so the now-stale
// translation falls back to the original and the row is recoverable by a backfill — while a
// non-content update leaves the translation columns untouched.
func TestBuildUpdateQuery_ClearsStaleTranslationOnContentChange(t *testing.T) {
	text := "updated text"
	lang := "de-DE"
	meta := json.RawMessage(`{"k":"v"}`)

	cases := []struct {
		name      string
		req       *models.UpdateFeedbackRecordRequest
		wantClear bool
	}{
		{"value_text change clears translation", &models.UpdateFeedbackRecordRequest{ValueText: &text}, true},
		{"language change clears translation", &models.UpdateFeedbackRecordRequest{Language: &lang}, true},
		{"non-content change keeps translation", &models.UpdateFeedbackRecordRequest{Metadata: meta}, false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			query, _, hasUpdates := buildUpdateQuery(testCase.req, uuid.New(), time.Now())
			if !hasUpdates {
				t.Fatal("buildUpdateQuery hasUpdates = false, want true")
			}

			gotClear := strings.Contains(query, "value_text_translated = CASE WHEN") &&
				strings.Contains(query, "translation_lang_key = CASE WHEN")
			if gotClear != testCase.wantClear {
				t.Fatalf("clears translation = %v, want %v\nquery: %s", gotClear, testCase.wantClear, query)
			}
		})
	}
}
