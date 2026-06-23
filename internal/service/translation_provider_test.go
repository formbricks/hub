package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
)

type mockTranslationInserter struct {
	calls []FeedbackTranslationArgs
	err   error
}

func (m *mockTranslationInserter) Insert(
	_ context.Context, args river.JobArgs, _ *river.InsertOpts,
) (*rivertype.JobInsertResult, error) {
	if a, ok := args.(FeedbackTranslationArgs); ok {
		m.calls = append(m.calls, a)
	}

	return &rivertype.JobInsertResult{}, m.err
}

type mockTargetResolver struct {
	target string
	err    error
}

func (m *mockTargetResolver) GetSettings(_ context.Context, tenantID string) (*models.TenantSettings, error) {
	if m.err != nil {
		return nil, m.err
	}

	return &models.TenantSettings{
		TenantID: tenantID,
		Settings: models.EnrichmentSettings{TargetLanguage: m.target},
	}, nil
}

func textRecord(valueText string) *models.FeedbackRecord {
	return &models.FeedbackRecord{
		ID:        uuid.New(),
		TenantID:  "org-1",
		FieldType: models.FieldTypeText,
		ValueText: &valueText,
	}
}

func newTestTranslationProvider(
	inserter RiverJobInserter, target string, resolveErr error,
) *TranslationProvider {
	resolver := &mockTargetResolver{target: target, err: resolveErr}

	return NewTranslationProvider(inserter, resolver, TranslationsQueueName, 3)
}

func TestTranslationProvider_CreateWithTextEnqueues(t *testing.T) {
	inserter := &mockTranslationInserter{}
	provider := newTestTranslationProvider(inserter, "de-DE", nil)
	record := textRecord("hello")

	provider.PublishEvent(context.Background(), Event{Type: datatypes.FeedbackRecordCreated, Data: record})

	if len(inserter.calls) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(inserter.calls))
	}

	got := inserter.calls[0]
	if got.FeedbackRecordID != record.ID || got.TargetLang != "de-DE" {
		t.Fatalf("args = %+v, want record %s / target de-DE", got, record.ID)
	}

	if got.ValueTextHash == "" || got.ValueTextHash == "empty" {
		t.Fatalf("ValueTextHash = %q, want a content hash", got.ValueTextHash)
	}
}

func TestTranslationProvider_UpdateWithValueTextChangeEnqueues(t *testing.T) {
	inserter := &mockTranslationInserter{}
	provider := newTestTranslationProvider(inserter, "de-DE", nil)

	provider.PublishEvent(context.Background(), Event{
		Type:          datatypes.FeedbackRecordUpdated,
		Data:          textRecord("hi"),
		ChangedFields: []string{"value_text"},
	})

	if len(inserter.calls) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(inserter.calls))
	}
}

func TestTranslationProvider_UpdateToEmptyEnqueuesForClear(t *testing.T) {
	inserter := &mockTranslationInserter{}
	provider := newTestTranslationProvider(inserter, "de-DE", nil)

	// value_text cleared by an edit: enqueue so the worker can clear a stale translation.
	provider.PublishEvent(context.Background(), Event{
		Type:          datatypes.FeedbackRecordUpdated,
		Data:          textRecord(""),
		ChangedFields: []string{"value_text"},
	})

	if len(inserter.calls) != 1 {
		t.Fatalf("enqueued %d jobs, want 1 (update-to-empty enqueues a clear)", len(inserter.calls))
	}

	if inserter.calls[0].ValueTextHash != "empty" {
		t.Fatalf("ValueTextHash = %q, want \"empty\"", inserter.calls[0].ValueTextHash)
	}
}

func TestTranslationProvider_LanguageChangeEnqueues(t *testing.T) {
	inserter := &mockTranslationInserter{}
	provider := newTestTranslationProvider(inserter, "en-US", nil)

	// Correcting the source language (value_text unchanged) must re-enqueue: the
	// translation depends on the source language.
	provider.PublishEvent(context.Background(), Event{
		Type:          datatypes.FeedbackRecordUpdated,
		Data:          textRecord("Bonjour"),
		ChangedFields: []string{"language"},
	})

	if len(inserter.calls) != 1 {
		t.Fatalf("enqueued %d jobs, want 1 (a language change re-enqueues)", len(inserter.calls))
	}
}

func TestTranslationContentHash_VariesBySourceLanguage(t *testing.T) {
	text := "Bonjour"

	if translationContentHash(&text, "fr") == translationContentHash(&text, "en") {
		t.Fatal("content hash must differ by source language so a language change re-translates")
	}

	empty := ""
	if got := translationContentHash(&empty, "fr"); got != "empty" {
		t.Fatalf("blank value_text hash = %q, want \"empty\"", got)
	}
}

func TestTranslationProvider_Skips(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		resolveErr error
		event      Event
	}{
		{
			name:   "empty value_text",
			target: "de-DE",
			event:  Event{Type: datatypes.FeedbackRecordCreated, Data: textRecord("   ")},
		},
		{
			name:   "non-text field",
			target: "de-DE",
			event: Event{Type: datatypes.FeedbackRecordCreated, Data: func() *models.FeedbackRecord {
				r := textRecord("hello")
				r.FieldType = models.FieldTypeCategorical

				return r
			}()},
		},
		{
			name:   "tenant has no target language",
			target: "",
			event:  Event{Type: datatypes.FeedbackRecordCreated, Data: textRecord("hello")},
		},
		{
			name:       "resolver error",
			target:     "de-DE",
			resolveErr: errors.New("boom"),
			event:      Event{Type: datatypes.FeedbackRecordCreated, Data: textRecord("hello")},
		},
		{
			name:   "update without value_text in changed fields",
			target: "de-DE",
			event: Event{
				Type:          datatypes.FeedbackRecordUpdated,
				Data:          textRecord("hi"),
				ChangedFields: []string{"metadata"},
			},
		},
		{
			name:   "event data is not a feedback record",
			target: "de-DE",
			event:  Event{Type: datatypes.FeedbackRecordCreated, Data: "not a record"},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			inserter := &mockTranslationInserter{}
			provider := newTestTranslationProvider(inserter, testCase.target, testCase.resolveErr)

			provider.PublishEvent(context.Background(), testCase.event)

			if len(inserter.calls) != 0 {
				t.Fatalf("enqueued %d jobs, want 0 (%s)", len(inserter.calls), testCase.name)
			}
		})
	}
}
