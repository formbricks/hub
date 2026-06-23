package workers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
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

type translationSetCall struct {
	translated *string
	langKey    string
}

type mockTranslationWorkerService struct {
	record   *models.FeedbackRecord
	getErr   error
	setErr   error
	setCalls []translationSetCall
}

func (m *mockTranslationWorkerService) GetFeedbackRecord(_ context.Context, _ uuid.UUID) (*models.FeedbackRecord, error) {
	return m.record, m.getErr
}

func (m *mockTranslationWorkerService) SetTranslation(
	_ context.Context, _ uuid.UUID, translated *string, langKey string,
) error {
	m.setCalls = append(m.setCalls, translationSetCall{translated: translated, langKey: langKey})

	return m.setErr
}

type stubTranslationClient struct {
	out   string
	err   error
	calls []service.TranslateRequest
}

func (s *stubTranslationClient) Translate(_ context.Context, req service.TranslateRequest) (string, error) {
	s.calls = append(s.calls, req)

	return s.out, s.err
}

func translationRecord(valueText, sourceLang string) *models.FeedbackRecord {
	record := &models.FeedbackRecord{
		ID:        uuid.Must(uuid.NewV7()),
		FieldType: models.FieldTypeText,
		ValueText: &valueText,
	}
	if sourceLang != "" {
		record.Language = &sourceLang
	}

	return record
}

func translationJob(targetLang string, attempt int) *river.Job[service.FeedbackTranslationArgs] {
	return &river.Job[service.FeedbackTranslationArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: 3},
		Args: service.FeedbackTranslationArgs{
			FeedbackRecordID: uuid.Must(uuid.NewV7()),
			TargetLang:       targetLang,
			ValueTextHash:    "hash",
		},
	}
}

func TestFeedbackTranslationWorker_TranslatesAndStores(t *testing.T) {
	svc := &mockTranslationWorkerService{record: translationRecord("Bonjour le monde", "fr")}
	client := &stubTranslationClient{out: "Hello world"}
	worker := NewFeedbackTranslationWorker(svc, client, nil)

	if err := worker.Work(context.Background(), translationJob("en-US", 1)); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if len(client.calls) != 1 || client.calls[0].SourceLang != "fr" || client.calls[0].TargetLang != "en-US" {
		t.Fatalf("client calls = %+v, want one fr->en-US", client.calls)
	}

	if len(svc.setCalls) != 1 || svc.setCalls[0].translated == nil ||
		*svc.setCalls[0].translated != "Hello world" || svc.setCalls[0].langKey != "en-US" {
		t.Fatalf("set calls = %+v, want translated 'Hello world' / en-US", svc.setCalls)
	}
}

func TestFeedbackTranslationWorker_SourceEqualsTargetCopies(t *testing.T) {
	svc := &mockTranslationWorkerService{record: translationRecord("Hello", "en-US")}
	client := &stubTranslationClient{out: "should-not-be-used"}
	worker := NewFeedbackTranslationWorker(svc, client, nil)

	if err := worker.Work(context.Background(), translationJob("en-GB", 1)); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if len(client.calls) != 0 {
		t.Fatalf("client called %d times, want 0 (source base == target base)", len(client.calls))
	}

	if len(svc.setCalls) != 1 || svc.setCalls[0].translated == nil || *svc.setCalls[0].translated != "Hello" {
		t.Fatalf("set calls = %+v, want copied 'Hello'", svc.setCalls)
	}
}

func TestFeedbackTranslationWorker_ClearsWhenValueTextEmpty(t *testing.T) {
	svc := &mockTranslationWorkerService{record: translationRecord("   ", "fr")}
	client := &stubTranslationClient{out: "x"}
	worker := NewFeedbackTranslationWorker(svc, client, nil)

	if err := worker.Work(context.Background(), translationJob("en-US", 1)); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if len(client.calls) != 0 {
		t.Fatalf("client called %d times, want 0 (no translation of empty value_text)", len(client.calls))
	}

	if len(svc.setCalls) != 1 || svc.setCalls[0].translated != nil {
		t.Fatalf("set calls = %+v, want one clear (nil translated)", svc.setCalls)
	}
}

func TestFeedbackTranslationWorker_UndeterminedSourceTranslates(t *testing.T) {
	// "und" (undetermined) must not be treated as matching the target — translate, not copy.
	svc := &mockTranslationWorkerService{record: translationRecord("Bonjour", "und")}
	client := &stubTranslationClient{out: "Hello"}
	worker := NewFeedbackTranslationWorker(svc, client, nil)

	if err := worker.Work(context.Background(), translationJob("en-US", 1)); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if len(client.calls) != 1 {
		t.Fatalf("client called %d times, want 1 (und source must translate, not copy)", len(client.calls))
	}
}

func TestFeedbackTranslationWorker_DifferentScriptTranslates(t *testing.T) {
	// zh-Hans and zh-Hant share a base language but not a script: must translate, not copy.
	svc := &mockTranslationWorkerService{record: translationRecord("simplified content", "zh-Hans")}
	client := &stubTranslationClient{out: "translated"}
	worker := NewFeedbackTranslationWorker(svc, client, nil)

	if err := worker.Work(context.Background(), translationJob("zh-Hant", 1)); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if len(client.calls) != 1 {
		t.Fatalf("client called %d times, want 1 (zh-Hans != zh-Hant must translate)", len(client.calls))
	}
}

func TestFeedbackTranslationWorker_NotFoundCompletes(t *testing.T) {
	metrics := newCountingTranslationMetrics()
	svc := &mockTranslationWorkerService{getErr: huberrors.NewNotFoundError("feedback record", "gone")}
	worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{}, metrics)

	if err := worker.Work(context.Background(), translationJob("en-US", 1)); err != nil {
		t.Fatalf("Work() error = %v, want nil (not-found completes)", err)
	}

	// A not-found GET is a benign delete/purge race: record it as skipped, never
	// failed_final (which would trip failure alerts) and not as a worker error.
	if metrics.outcomes["skipped"] != 1 || metrics.outcomes["failed_final"] != 0 {
		t.Fatalf("skipped=%d failed_final=%d, want 1/0", metrics.outcomes["skipped"], metrics.outcomes["failed_final"])
	}

	if metrics.workerErr["get_record_failed"] != 0 {
		t.Fatalf("get_record_failed=%d, want 0 (not-found is not a worker error)", metrics.workerErr["get_record_failed"])
	}
}

func TestFeedbackTranslationWorker_ProviderErrorRetriesThenFails(t *testing.T) {
	svc := &mockTranslationWorkerService{record: translationRecord("Bonjour", "fr")}
	client := &stubTranslationClient{err: errors.New("api down")}
	worker := NewFeedbackTranslationWorker(svc, client, nil)

	if err := worker.Work(context.Background(), translationJob("en-US", 1)); err == nil {
		t.Fatal("Work() = nil on non-final attempt, want retry error")
	}

	if err := worker.Work(context.Background(), translationJob("en-US", 3)); err == nil {
		t.Fatal("Work() = nil on final attempt, want error")
	}

	if len(svc.setCalls) != 0 {
		t.Fatal("set called despite provider error")
	}
}

func TestFeedbackTranslationWorker_TenantWriteConflictRetries(t *testing.T) {
	svc := &mockTranslationWorkerService{
		record: translationRecord("Bonjour", "fr"),
		setErr: huberrors.NewTenantWriteConflictError("purge in progress"),
	}
	worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{out: "Hi"}, nil)

	if err := worker.Work(context.Background(), translationJob("en-US", 1)); err == nil {
		t.Fatal("Work() = nil, want retry on tenant write conflict")
	}
}

func TestFeedbackTranslationWorker_RecordGoneOnWriteCompletes(t *testing.T) {
	svc := &mockTranslationWorkerService{
		record: translationRecord("Bonjour", "fr"),
		setErr: huberrors.NewNotFoundError("feedback record", "gone"),
	}
	worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{out: "Hi"}, nil)

	if err := worker.Work(context.Background(), translationJob("en-US", 1)); err != nil {
		t.Fatalf("Work() error = %v, want nil (record gone before write completes)", err)
	}
}

func TestFeedbackTranslationWorker_RecordsMetrics(t *testing.T) {
	t.Run("success records outcome and duration", func(t *testing.T) {
		metrics := newCountingTranslationMetrics()
		svc := &mockTranslationWorkerService{record: translationRecord("Bonjour", "fr")}
		worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{out: "Hi"}, metrics)

		if err := worker.Work(context.Background(), translationJob("en-US", 1)); err != nil {
			t.Fatalf("Work() error = %v", err)
		}

		if metrics.outcomes["success"] != 1 || metrics.durations["success"] != 1 {
			t.Fatalf("success outcome=%d duration=%d, want 1/1",
				metrics.outcomes["success"], metrics.durations["success"])
		}
	})

	t.Run("non-text field records skipped", func(t *testing.T) {
		metrics := newCountingTranslationMetrics()
		record := translationRecord("hello", "fr")
		record.FieldType = models.FieldTypeCategorical
		worker := NewFeedbackTranslationWorker(&mockTranslationWorkerService{record: record}, &stubTranslationClient{}, metrics)

		if err := worker.Work(context.Background(), translationJob("en-US", 1)); err != nil {
			t.Fatalf("Work() error = %v", err)
		}

		if metrics.outcomes["skipped"] != 1 {
			t.Fatalf("skipped = %d, want 1", metrics.outcomes["skipped"])
		}
	})

	t.Run("empty value_text clear records success", func(t *testing.T) {
		metrics := newCountingTranslationMetrics()
		svc := &mockTranslationWorkerService{record: translationRecord("   ", "fr")}
		worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{}, metrics)

		if err := worker.Work(context.Background(), translationJob("en-US", 1)); err != nil {
			t.Fatalf("Work() error = %v", err)
		}

		if metrics.outcomes["success"] != 1 {
			t.Fatalf("clear success = %d, want 1", metrics.outcomes["success"])
		}
	})

	t.Run("provider failure records worker error, retry then failed_final", func(t *testing.T) {
		metrics := newCountingTranslationMetrics()
		svc := &mockTranslationWorkerService{record: translationRecord("Bonjour", "fr")}
		worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{err: errors.New("api down")}, metrics)

		_ = worker.Work(context.Background(), translationJob("en-US", 1)) // non-final -> retry
		_ = worker.Work(context.Background(), translationJob("en-US", 3)) // final -> failed_final

		if metrics.workerErr["translation_api_failed"] != 2 {
			t.Fatalf("translation_api_failed = %d, want 2", metrics.workerErr["translation_api_failed"])
		}

		if metrics.outcomes["retry"] != 1 || metrics.outcomes["failed_final"] != 1 {
			t.Fatalf("retry=%d failed_final=%d, want 1/1",
				metrics.outcomes["retry"], metrics.outcomes["failed_final"])
		}
	})

	t.Run("get record failure records get_record_failed and failed_final", func(t *testing.T) {
		metrics := newCountingTranslationMetrics()
		svc := &mockTranslationWorkerService{getErr: errors.New("db down")}
		worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{}, metrics)

		if err := worker.Work(context.Background(), translationJob("en-US", 1)); err == nil {
			t.Fatal("Work() = nil, want error on get failure")
		}

		if metrics.workerErr["get_record_failed"] != 1 || metrics.outcomes["failed_final"] != 1 {
			t.Fatalf("get_record_failed=%d failed_final=%d, want 1/1",
				metrics.workerErr["get_record_failed"], metrics.outcomes["failed_final"])
		}
	})

	t.Run("tenant write conflict records worker error and retry", func(t *testing.T) {
		metrics := newCountingTranslationMetrics()
		svc := &mockTranslationWorkerService{
			record: translationRecord("Bonjour", "fr"),
			setErr: huberrors.NewTenantWriteConflictError("purge"),
		}
		worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{out: "Hi"}, metrics)

		_ = worker.Work(context.Background(), translationJob("en-US", 1))

		if metrics.workerErr["tenant_write_conflict"] != 1 || metrics.outcomes["retry"] != 1 {
			t.Fatalf("tenant_write_conflict=%d retry=%d, want 1/1",
				metrics.workerErr["tenant_write_conflict"], metrics.outcomes["retry"])
		}
	})

	t.Run("set update failure records update_failed", func(t *testing.T) {
		metrics := newCountingTranslationMetrics()
		svc := &mockTranslationWorkerService{
			record: translationRecord("Bonjour", "fr"),
			setErr: errors.New("update boom"),
		}
		worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{out: "Hi"}, metrics)

		_ = worker.Work(context.Background(), translationJob("en-US", 1))

		if metrics.workerErr["update_failed"] != 1 || metrics.outcomes["failed_final"] != 1 {
			t.Fatalf("update_failed=%d failed_final=%d, want 1/1",
				metrics.workerErr["update_failed"], metrics.outcomes["failed_final"])
		}
	})

	t.Run("record gone on write records skipped", func(t *testing.T) {
		metrics := newCountingTranslationMetrics()
		svc := &mockTranslationWorkerService{
			record: translationRecord("Bonjour", "fr"),
			setErr: huberrors.NewNotFoundError("feedback record", "gone"),
		}
		worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{out: "Hi"}, metrics)

		if err := worker.Work(context.Background(), translationJob("en-US", 1)); err != nil {
			t.Fatalf("Work() error = %v, want nil", err)
		}

		if metrics.outcomes["skipped"] != 1 {
			t.Fatalf("skipped = %d, want 1", metrics.outcomes["skipped"])
		}
	})
}
