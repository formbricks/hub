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

// countingEmotionsMetrics is an in-memory observability.EmotionsMetrics for asserting which
// metrics fired (and with which reason/status labels).
type countingEmotionsMetrics struct {
	enqueued    int64
	providerErr map[string]int
	outcomes    map[string]int
	workerErr   map[string]int
	durations   map[string]int
}

func newCountingEmotionsMetrics() *countingEmotionsMetrics {
	return &countingEmotionsMetrics{
		providerErr: map[string]int{},
		outcomes:    map[string]int{},
		workerErr:   map[string]int{},
		durations:   map[string]int{},
	}
}

func (m *countingEmotionsMetrics) RecordJobsEnqueued(_ context.Context, count int64) {
	m.enqueued += count
}

func (m *countingEmotionsMetrics) RecordProviderError(_ context.Context, reason string) {
	m.providerErr[reason]++
}

func (m *countingEmotionsMetrics) RecordEmotionsOutcome(_ context.Context, status string) {
	m.outcomes[status]++
}

func (m *countingEmotionsMetrics) RecordWorkerError(_ context.Context, reason string) {
	m.workerErr[reason]++
}

func (m *countingEmotionsMetrics) RecordEmotionsDuration(_ context.Context, _ time.Duration, status string) {
	m.durations[status]++
}

var _ observability.EmotionsMetrics = (*countingEmotionsMetrics)(nil)

type mockEmotionsWorkerService struct {
	record   *models.FeedbackRecord
	getErr   error
	setErr   error
	setCalls [][]models.EmotionValue
}

func (m *mockEmotionsWorkerService) GetFeedbackRecord(_ context.Context, _ uuid.UUID) (*models.FeedbackRecord, error) {
	return m.record, m.getErr
}

func (m *mockEmotionsWorkerService) SetEmotions(
	_ context.Context, _ uuid.UUID, emotions []models.EmotionValue,
) error {
	m.setCalls = append(m.setCalls, emotions)

	return m.setErr
}

type stubEmotionsClient struct {
	result service.EmotionsResult
	err    error
	calls  int
}

func (s *stubEmotionsClient) Classify(_ context.Context, _, _ string) (service.EmotionsResult, error) {
	s.calls++

	return s.result, s.err
}

func emotionsTextRecord(valueText *string) *models.FeedbackRecord {
	return &models.FeedbackRecord{ID: uuid.Must(uuid.NewV7()), FieldType: models.FieldTypeText, ValueText: valueText}
}

func emotionsJob(attempt int) *river.Job[service.FeedbackEmotionsArgs] {
	return &river.Job[service.FeedbackEmotionsArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: 3},
		Args:   service.FeedbackEmotionsArgs{FeedbackRecordID: uuid.Must(uuid.NewV7())},
	}
}

func TestFeedbackEmotionsWorker_Success(t *testing.T) {
	text := "I am thrilled and a little scared"
	metrics := newCountingEmotionsMetrics()
	svc := &mockEmotionsWorkerService{record: emotionsTextRecord(&text)}
	client := &stubEmotionsClient{result: service.EmotionsResult{
		Labels: []models.EmotionValue{models.EmotionJoy, models.EmotionFear},
	}}
	worker := NewFeedbackEmotionsWorker(svc, client, metrics)

	if err := worker.Work(context.Background(), emotionsJob(1)); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if client.calls != 1 {
		t.Fatalf("Classify calls = %d, want 1", client.calls)
	}

	if len(svc.setCalls) != 1 || len(svc.setCalls[0]) != 2 ||
		svc.setCalls[0][0] != models.EmotionJoy || svc.setCalls[0][1] != models.EmotionFear {
		t.Fatalf("setCalls = %+v, want one call with [joy fear]", svc.setCalls)
	}

	if metrics.outcomes["success"] != 1 {
		t.Fatalf("success outcomes = %d, want 1", metrics.outcomes["success"])
	}
}

func TestFeedbackEmotionsWorker_EmptyResultClears(t *testing.T) {
	// The classifier returning no emotions is a valid outcome: the column is cleared, not left stale.
	text := "the weather forecast for tomorrow"
	metrics := newCountingEmotionsMetrics()
	svc := &mockEmotionsWorkerService{record: emotionsTextRecord(&text)}
	client := &stubEmotionsClient{result: service.EmotionsResult{Labels: nil}}
	worker := NewFeedbackEmotionsWorker(svc, client, metrics)

	if err := worker.Work(context.Background(), emotionsJob(1)); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if client.calls != 1 {
		t.Fatalf("Classify calls = %d, want 1", client.calls)
	}

	if len(svc.setCalls) != 1 || len(svc.setCalls[0]) != 0 {
		t.Fatalf("setCalls = %+v, want one clear (empty set)", svc.setCalls)
	}

	if metrics.outcomes["success"] != 1 {
		t.Fatalf("success outcomes = %d, want 1", metrics.outcomes["success"])
	}
}

func TestFeedbackEmotionsWorker_EmptyValueTextClears(t *testing.T) {
	empty := "   "

	for name, record := range map[string]*models.FeedbackRecord{
		"nil value_text":   emotionsTextRecord(nil),
		"blank value_text": emotionsTextRecord(&empty),
	} {
		t.Run(name, func(t *testing.T) {
			svc := &mockEmotionsWorkerService{record: record}
			client := &stubEmotionsClient{}
			worker := NewFeedbackEmotionsWorker(svc, client, newCountingEmotionsMetrics())

			if err := worker.Work(context.Background(), emotionsJob(1)); err != nil {
				t.Fatalf("Work() error = %v", err)
			}

			if client.calls != 0 {
				t.Fatalf("Classify calls = %d, want 0 (empty text is not classified)", client.calls)
			}

			if len(svc.setCalls) != 1 || len(svc.setCalls[0]) != 0 {
				t.Fatalf("setCalls = %+v, want one clear (empty set)", svc.setCalls)
			}
		})
	}
}

func TestFeedbackEmotionsWorker_NonTextFieldSkips(t *testing.T) {
	svc := &mockEmotionsWorkerService{record: &models.FeedbackRecord{ID: uuid.Must(uuid.NewV7()), FieldType: models.FieldTypeNumber}}
	client := &stubEmotionsClient{}
	metrics := newCountingEmotionsMetrics()
	worker := NewFeedbackEmotionsWorker(svc, client, metrics)

	if err := worker.Work(context.Background(), emotionsJob(1)); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if client.calls != 0 || len(svc.setCalls) != 0 {
		t.Fatalf("non-text field must not classify or write: calls=%d sets=%d", client.calls, len(svc.setCalls))
	}

	if metrics.outcomes["skipped"] != 1 {
		t.Fatalf("skipped outcomes = %d, want 1", metrics.outcomes["skipped"])
	}
}

func TestFeedbackEmotionsWorker_RecordGoneSkips(t *testing.T) {
	svc := &mockEmotionsWorkerService{getErr: huberrors.ErrNotFound}
	metrics := newCountingEmotionsMetrics()
	worker := NewFeedbackEmotionsWorker(svc, &stubEmotionsClient{}, metrics)

	if err := worker.Work(context.Background(), emotionsJob(1)); err != nil {
		t.Fatalf("Work() error = %v, want nil (a record gone before classify is a benign skip)", err)
	}

	if metrics.outcomes["skipped"] != 1 || metrics.outcomes["failed_final"] != 0 {
		t.Fatalf("skipped=%d failed_final=%d, want 1/0", metrics.outcomes["skipped"], metrics.outcomes["failed_final"])
	}
}

func TestFeedbackEmotionsWorker_RateLimitSnoozes(t *testing.T) {
	text := "Bonjour"
	metrics := newCountingEmotionsMetrics()
	svc := &mockEmotionsWorkerService{record: emotionsTextRecord(&text)}
	client := &stubEmotionsClient{err: huberrors.NewRateLimitError(45*time.Second, errors.New("429"))}
	worker := NewFeedbackEmotionsWorker(svc, client, metrics)

	err := worker.Work(context.Background(), emotionsJob(1))

	var snooze *river.JobSnoozeError
	if !errors.As(err, &snooze) {
		t.Fatalf("Work() error = %v, want a river snooze error", err)
	}

	if snooze.Duration != 45*time.Second {
		t.Fatalf("snooze duration = %v, want 45s (provider retry-after)", snooze.Duration)
	}

	if len(svc.setCalls) != 0 {
		t.Fatalf("set called %d times on rate limit, want 0 (work deferred)", len(svc.setCalls))
	}

	if metrics.workerErr["rate_limited"] != 1 || metrics.outcomes["retry"] != 1 {
		t.Fatalf("rate_limited=%d retry=%d, want 1/1", metrics.workerErr["rate_limited"], metrics.outcomes["retry"])
	}
}

func TestFeedbackEmotionsWorker_ClassifyFailsOnFinalAttempt(t *testing.T) {
	text := "Bonjour"
	metrics := newCountingEmotionsMetrics()
	svc := &mockEmotionsWorkerService{record: emotionsTextRecord(&text)}
	client := &stubEmotionsClient{err: errors.New("provider down")}
	worker := NewFeedbackEmotionsWorker(svc, client, metrics)

	err := worker.Work(context.Background(), emotionsJob(3)) // attempt == MaxAttempts
	if err == nil {
		t.Fatal("Work() error = nil, want a final-attempt failure")
	}

	if metrics.outcomes["failed_final"] != 1 || metrics.workerErr["emotions_api_failed"] != 1 {
		t.Fatalf("failed_final=%d emotions_api_failed=%d, want 1/1",
			metrics.outcomes["failed_final"], metrics.workerErr["emotions_api_failed"])
	}
}

func TestFeedbackEmotionsWorker_SetEmotionsErrors(t *testing.T) {
	text := "Great"
	result := service.EmotionsResult{Labels: []models.EmotionValue{models.EmotionJoy}}

	t.Run("record gone before write is a benign skip", func(t *testing.T) {
		svc := &mockEmotionsWorkerService{record: emotionsTextRecord(&text), setErr: huberrors.ErrNotFound}
		metrics := newCountingEmotionsMetrics()
		worker := NewFeedbackEmotionsWorker(svc, &stubEmotionsClient{result: result}, metrics)

		if err := worker.Work(context.Background(), emotionsJob(1)); err != nil {
			t.Fatalf("Work() error = %v, want nil", err)
		}

		if metrics.outcomes["skipped"] != 1 {
			t.Fatalf("skipped = %d, want 1", metrics.outcomes["skipped"])
		}
	})

	t.Run("tenant write conflict retries", func(t *testing.T) {
		svc := &mockEmotionsWorkerService{record: emotionsTextRecord(&text), setErr: huberrors.ErrTenantWriteConflict}
		metrics := newCountingEmotionsMetrics()
		worker := NewFeedbackEmotionsWorker(svc, &stubEmotionsClient{result: result}, metrics)

		if err := worker.Work(context.Background(), emotionsJob(1)); err == nil {
			t.Fatal("Work() error = nil, want a retryable error")
		}

		if metrics.workerErr["tenant_write_conflict"] != 1 || metrics.outcomes["retry"] != 1 {
			t.Fatalf("tenant_write_conflict=%d retry=%d, want 1/1",
				metrics.workerErr["tenant_write_conflict"], metrics.outcomes["retry"])
		}
	})

	t.Run("other write error retries, failing on the final attempt", func(t *testing.T) {
		svc := &mockEmotionsWorkerService{record: emotionsTextRecord(&text), setErr: errors.New("db unavailable")}
		metrics := newCountingEmotionsMetrics()
		worker := NewFeedbackEmotionsWorker(svc, &stubEmotionsClient{result: result}, metrics)

		if err := worker.Work(context.Background(), emotionsJob(1)); err == nil {
			t.Fatal("Work() error = nil, want a failure")
		}

		if metrics.workerErr["update_failed"] != 1 || metrics.outcomes["retry"] != 1 || metrics.outcomes["failed_final"] != 0 {
			t.Fatalf("update_failed=%d retry=%d failed_final=%d, want 1/1/0 (transient write blip must not read as final)",
				metrics.workerErr["update_failed"], metrics.outcomes["retry"], metrics.outcomes["failed_final"])
		}

		if err := worker.Work(context.Background(), emotionsJob(3)); err == nil {
			t.Fatal("Work() error = nil, want a failure on the final attempt")
		}

		if metrics.outcomes["failed_final"] != 1 {
			t.Fatalf("failed_final=%d after the final attempt, want 1", metrics.outcomes["failed_final"])
		}
	})
}
