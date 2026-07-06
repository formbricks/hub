package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// countingTranslationMetrics is an in-memory observability.TranslationMetrics for asserting
// which metrics fired (and with which reason/status labels).
type countingTranslationMetrics struct {
	enqueued    int64
	providerErr map[string]int
	outcomes    map[string]int
	workerErr   map[string]int
	durations   map[string]int
}

func newCountingTranslationMetrics() *countingTranslationMetrics {
	return &countingTranslationMetrics{
		providerErr: map[string]int{},
		outcomes:    map[string]int{},
		workerErr:   map[string]int{},
		durations:   map[string]int{},
	}
}

func (m *countingTranslationMetrics) RecordJobsEnqueued(_ context.Context, count int64) {
	m.enqueued += count
}

func (m *countingTranslationMetrics) RecordProviderError(_ context.Context, reason string) {
	m.providerErr[reason]++
}

func (m *countingTranslationMetrics) RecordTranslationOutcome(_ context.Context, status string) {
	m.outcomes[status]++
}

func (m *countingTranslationMetrics) RecordWorkerError(_ context.Context, reason string) {
	m.workerErr[reason]++
}

func (m *countingTranslationMetrics) RecordTranslationDuration(_ context.Context, _ time.Duration, status string) {
	m.durations[status]++
}

var _ observability.TranslationMetrics = (*countingTranslationMetrics)(nil)

type mockTranslationInserter struct {
	calls []FeedbackTranslationArgs
	err   error
	// skipEvery > 0 marks every skipEvery-th insert as UniqueSkippedAsDuplicate, for
	// exercising the backfill's truthful enqueued-vs-skipped accounting.
	skipEvery int
}

func (m *mockTranslationInserter) Insert(
	_ context.Context, args river.JobArgs, _ *river.InsertOpts,
) (*rivertype.JobInsertResult, error) {
	if a, ok := args.(FeedbackTranslationArgs); ok {
		m.calls = append(m.calls, a)
	}

	result := &rivertype.JobInsertResult{}
	if m.skipEvery > 0 && len(m.calls)%m.skipEvery == 0 {
		result.UniqueSkippedAsDuplicate = true
	}

	return result, m.err
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

	return NewTranslationProvider(inserter, resolver, TranslationsQueueName, 3, "", nil)
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

	eventID := uuid.Must(uuid.NewV7())
	provider.PublishEvent(context.Background(), Event{
		ID:            eventID,
		Type:          datatypes.FeedbackRecordUpdated,
		Data:          textRecord("hi"),
		ChangedFields: []string{"value_text"},
	})

	if len(inserter.calls) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(inserter.calls))
	}

	if inserter.calls[0].EventID != eventID {
		t.Fatalf("EventID = %v, want %v (event id carried into the job args)", inserter.calls[0].EventID, eventID)
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

func TestTranslationProvider_FallsBackToDefaultLanguage(t *testing.T) {
	t.Run("tenant has no target uses the configured default", func(t *testing.T) {
		inserter := &mockTranslationInserter{}
		provider := NewTranslationProvider(
			inserter, &mockTargetResolver{target: ""}, TranslationsQueueName, 3, "es-ES", nil)

		provider.PublishEvent(context.Background(), Event{Type: datatypes.FeedbackRecordCreated, Data: textRecord("hola")})

		if len(inserter.calls) != 1 {
			t.Fatalf("enqueued %d jobs, want 1 (default language applies when tenant has none)", len(inserter.calls))
		}

		if got := inserter.calls[0].TargetLang; got != "es-ES" {
			t.Fatalf("TargetLang = %q, want es-ES (default)", got)
		}
	})

	t.Run("tenant target overrides the default", func(t *testing.T) {
		inserter := &mockTranslationInserter{}
		provider := NewTranslationProvider(
			inserter, &mockTargetResolver{target: "de-DE"}, TranslationsQueueName, 3, "es-ES", nil)

		provider.PublishEvent(context.Background(), Event{Type: datatypes.FeedbackRecordCreated, Data: textRecord("hello")})

		if len(inserter.calls) != 1 || inserter.calls[0].TargetLang != "de-DE" {
			t.Fatalf("calls = %+v, want one job with target de-DE (tenant overrides default)", inserter.calls)
		}
	})
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

func TestTranslationProvider_RecordsMetrics(t *testing.T) {
	t.Run("enqueue success increments jobs", func(t *testing.T) {
		metrics := newCountingTranslationMetrics()
		inserter := &mockTranslationInserter{}
		provider := NewTranslationProvider(
			inserter, &mockTargetResolver{target: "de-DE"}, TranslationsQueueName, 3, "", metrics)

		provider.PublishEvent(context.Background(), Event{Type: datatypes.FeedbackRecordCreated, Data: textRecord("hello")})

		if metrics.enqueued != 1 {
			t.Fatalf("enqueued = %d, want 1", metrics.enqueued)
		}
	})

	t.Run("settings read failure records provider error", func(t *testing.T) {
		metrics := newCountingTranslationMetrics()
		resolver := &mockTargetResolver{target: "de-DE", err: errors.New("boom")}
		provider := NewTranslationProvider(&mockTranslationInserter{}, resolver, TranslationsQueueName, 3, "", metrics)

		provider.PublishEvent(context.Background(), Event{Type: datatypes.FeedbackRecordCreated, Data: textRecord("hello")})

		if metrics.providerErr["settings_read_failed"] != 1 {
			t.Fatalf("settings_read_failed = %d, want 1", metrics.providerErr["settings_read_failed"])
		}
	})

	t.Run("enqueue failure records provider error and no enqueue", func(t *testing.T) {
		metrics := newCountingTranslationMetrics()
		inserter := &mockTranslationInserter{err: errors.New("insert failed")}
		provider := NewTranslationProvider(
			inserter, &mockTargetResolver{target: "de-DE"}, TranslationsQueueName, 3, "", metrics)

		provider.PublishEvent(context.Background(), Event{Type: datatypes.FeedbackRecordCreated, Data: textRecord("hello")})

		if metrics.providerErr["enqueue_failed"] != 1 {
			t.Fatalf("enqueue_failed = %d, want 1", metrics.providerErr["enqueue_failed"])
		}

		if metrics.enqueued != 0 {
			t.Fatalf("enqueued = %d, want 0 on insert failure", metrics.enqueued)
		}
	})
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
