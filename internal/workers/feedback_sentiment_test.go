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

// countingSentimentMetrics is an in-memory observability.SentimentMetrics for asserting which
// metrics fired (and with which reason/status labels).
type countingSentimentMetrics struct {
	enqueued    int64
	providerErr map[string]int
	outcomes    map[string]int
	workerErr   map[string]int
	durations   map[string]int
}

func newCountingSentimentMetrics() *countingSentimentMetrics {
	return &countingSentimentMetrics{
		providerErr: map[string]int{},
		outcomes:    map[string]int{},
		workerErr:   map[string]int{},
		durations:   map[string]int{},
	}
}

func (m *countingSentimentMetrics) RecordJobsEnqueued(_ context.Context, count int64) {
	m.enqueued += count
}

func (m *countingSentimentMetrics) RecordProviderError(_ context.Context, reason string) {
	m.providerErr[reason]++
}

func (m *countingSentimentMetrics) RecordSentimentOutcome(_ context.Context, status string) {
	m.outcomes[status]++
}

func (m *countingSentimentMetrics) RecordWorkerError(_ context.Context, reason string) {
	m.workerErr[reason]++
}

func (m *countingSentimentMetrics) RecordSentimentDuration(_ context.Context, _ time.Duration, status string) {
	m.durations[status]++
}

var _ observability.SentimentMetrics = (*countingSentimentMetrics)(nil)

type sentimentSetCall struct {
	label *models.SentimentValue
	score *float64
}

type mockSentimentWorkerService struct {
	record   *models.FeedbackRecord
	getErr   error
	setErr   error
	setCalls []sentimentSetCall
}

func (m *mockSentimentWorkerService) GetFeedbackRecord(_ context.Context, _ uuid.UUID) (*models.FeedbackRecord, error) {
	return m.record, m.getErr
}

func (m *mockSentimentWorkerService) SetSentiment(
	_ context.Context, _ uuid.UUID, sentiment *models.SentimentValue, score *float64,
	_ func(valueText *string) bool,
) error {
	m.setCalls = append(m.setCalls, sentimentSetCall{label: sentiment, score: score})

	return m.setErr
}

type stubSentimentClient struct {
	result service.SentimentResult
	err    error
	calls  int
}

func (s *stubSentimentClient) Classify(_ context.Context, _, _ string) (service.SentimentResult, error) {
	s.calls++

	return s.result, s.err
}

// stubSentimentSettings is a tenantSettingsReader for the worker's per-directory gate. A nil enabled
// pointer means the tenant default (on); a non-nil err simulates a settings-read failure.
type stubSentimentSettings struct {
	enabled *bool
	err     error
}

func (s stubSentimentSettings) GetSettings(_ context.Context, tenantID string) (*models.TenantSettings, error) {
	if s.err != nil {
		return nil, s.err
	}

	return &models.TenantSettings{TenantID: tenantID, Settings: models.EnrichmentSettings{SentimentEnabled: s.enabled}}, nil
}

func sentimentTextRecord(valueText *string) *models.FeedbackRecord {
	return &models.FeedbackRecord{ID: uuid.Must(uuid.NewV7()), FieldType: models.FieldTypeText, ValueText: valueText}
}

func sentimentJob(attempt int) *river.Job[service.FeedbackSentimentArgs] {
	return &river.Job[service.FeedbackSentimentArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: 3},
		Args:   service.FeedbackSentimentArgs{FeedbackRecordID: uuid.Must(uuid.NewV7())},
	}
}

func TestFeedbackSentimentWorker_Success(t *testing.T) {
	text := "Great product"
	metrics := newCountingSentimentMetrics()
	svc := &mockSentimentWorkerService{record: sentimentTextRecord(&text)}
	client := &stubSentimentClient{result: service.SentimentResult{Label: models.SentimentPositive, Score: 0.5}}
	worker := NewFeedbackSentimentWorker(svc, stubSentimentSettings{}, client, metrics)

	if err := worker.Work(context.Background(), sentimentJob(1)); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if client.calls != 1 {
		t.Fatalf("Classify calls = %d, want 1", client.calls)
	}

	if len(svc.setCalls) != 1 || svc.setCalls[0].label == nil || *svc.setCalls[0].label != models.SentimentPositive {
		t.Fatalf("setCalls = %+v, want one call with label positive", svc.setCalls)
	}

	if svc.setCalls[0].score == nil || *svc.setCalls[0].score != 0.5 {
		t.Fatalf("stored score = %v, want 0.5", svc.setCalls[0].score)
	}

	if metrics.outcomes["success"] != 1 {
		t.Fatalf("success outcomes = %d, want 1", metrics.outcomes["success"])
	}
}

func TestFeedbackSentimentWorker_EmptyValueTextClears(t *testing.T) {
	empty := "   "

	for name, record := range map[string]*models.FeedbackRecord{
		"nil value_text":   sentimentTextRecord(nil),
		"blank value_text": sentimentTextRecord(&empty),
	} {
		t.Run(name, func(t *testing.T) {
			svc := &mockSentimentWorkerService{record: record}
			client := &stubSentimentClient{}
			worker := NewFeedbackSentimentWorker(svc, stubSentimentSettings{}, client, newCountingSentimentMetrics())

			if err := worker.Work(context.Background(), sentimentJob(1)); err != nil {
				t.Fatalf("Work() error = %v", err)
			}

			if client.calls != 0 {
				t.Fatalf("Classify calls = %d, want 0 (empty text is not classified)", client.calls)
			}

			if len(svc.setCalls) != 1 || svc.setCalls[0].label != nil || svc.setCalls[0].score != nil {
				t.Fatalf("setCalls = %+v, want one clear (nil, nil)", svc.setCalls)
			}
		})
	}
}

func TestFeedbackSentimentWorker_NonTextFieldSkips(t *testing.T) {
	svc := &mockSentimentWorkerService{record: &models.FeedbackRecord{ID: uuid.Must(uuid.NewV7()), FieldType: models.FieldTypeNumber}}
	client := &stubSentimentClient{}
	metrics := newCountingSentimentMetrics()
	worker := NewFeedbackSentimentWorker(svc, stubSentimentSettings{}, client, metrics)

	if err := worker.Work(context.Background(), sentimentJob(1)); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if client.calls != 0 || len(svc.setCalls) != 0 {
		t.Fatalf("non-text field must not classify or write: calls=%d sets=%d", client.calls, len(svc.setCalls))
	}

	if metrics.outcomes["skipped"] != 1 {
		t.Fatalf("skipped outcomes = %d, want 1", metrics.outcomes["skipped"])
	}
}

func TestFeedbackSentimentWorker_RecordGoneSkips(t *testing.T) {
	svc := &mockSentimentWorkerService{getErr: huberrors.ErrNotFound}
	metrics := newCountingSentimentMetrics()
	worker := NewFeedbackSentimentWorker(svc, stubSentimentSettings{}, &stubSentimentClient{}, metrics)

	if err := worker.Work(context.Background(), sentimentJob(1)); err != nil {
		t.Fatalf("Work() error = %v, want nil (a record gone before classify is a benign skip)", err)
	}

	if metrics.outcomes["skipped"] != 1 || metrics.outcomes["failed_final"] != 0 {
		t.Fatalf("skipped=%d failed_final=%d, want 1/0", metrics.outcomes["skipped"], metrics.outcomes["failed_final"])
	}
}

func TestFeedbackSentimentWorker_GetRecordFailsRetriesThenFailsFinal(t *testing.T) {
	// A non-not-found read error is transient: the worker retries while attempts remain and only
	// counts as failed_final on the last attempt, so failed_final is not overcounted.
	metrics := newCountingSentimentMetrics()
	svc := &mockSentimentWorkerService{getErr: errors.New("db unavailable")}
	worker := NewFeedbackSentimentWorker(svc, stubSentimentSettings{}, &stubSentimentClient{}, metrics)

	if err := worker.Work(context.Background(), sentimentJob(1)); err == nil {
		t.Fatal("Work() error = nil, want a get-record failure returned for retry")
	}

	if metrics.workerErr["get_record_failed"] != 1 || metrics.outcomes["retry"] != 1 || metrics.outcomes["failed_final"] != 0 {
		t.Fatalf("get_record_failed=%d retry=%d failed_final=%d, want 1/1/0 (transient read blip must not read as final)",
			metrics.workerErr["get_record_failed"], metrics.outcomes["retry"], metrics.outcomes["failed_final"])
	}

	if err := worker.Work(context.Background(), sentimentJob(3)); err == nil {
		t.Fatal("Work() error = nil, want a failure on the final attempt")
	}

	if metrics.outcomes["failed_final"] != 1 {
		t.Fatalf("failed_final=%d after the final attempt, want 1", metrics.outcomes["failed_final"])
	}
}

func TestFeedbackSentimentWorker_RateLimitSnoozes(t *testing.T) {
	text := "Bonjour"
	metrics := newCountingSentimentMetrics()
	svc := &mockSentimentWorkerService{record: sentimentTextRecord(&text)}
	client := &stubSentimentClient{err: huberrors.NewRateLimitError(45*time.Second, errors.New("429"))}
	worker := NewFeedbackSentimentWorker(svc, stubSentimentSettings{}, client, metrics)

	err := worker.Work(context.Background(), sentimentJob(1))

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

	if metrics.workerErr["rate_limited"] != 1 || metrics.outcomes["retry"] != 1 || metrics.outcomes["failed_final"] != 0 {
		t.Fatalf("rate_limited=%d retry=%d failed_final=%d, want 1/1/0",
			metrics.workerErr["rate_limited"], metrics.outcomes["retry"], metrics.outcomes["failed_final"])
	}
}

func TestFeedbackSentimentWorker_ClassifyFailsOnFinalAttempt(t *testing.T) {
	text := "Bonjour"
	metrics := newCountingSentimentMetrics()
	svc := &mockSentimentWorkerService{record: sentimentTextRecord(&text)}
	client := &stubSentimentClient{err: errors.New("provider down")}
	worker := NewFeedbackSentimentWorker(svc, stubSentimentSettings{}, client, metrics)

	err := worker.Work(context.Background(), sentimentJob(3)) // attempt == MaxAttempts
	if err == nil {
		t.Fatal("Work() error = nil, want a final-attempt failure")
	}

	if metrics.outcomes["failed_final"] != 1 || metrics.workerErr["sentiment_api_failed"] != 1 {
		t.Fatalf("failed_final=%d sentiment_api_failed=%d, want 1/1",
			metrics.outcomes["failed_final"], metrics.workerErr["sentiment_api_failed"])
	}
}

func TestFeedbackSentimentWorker_SetSentimentErrors(t *testing.T) {
	text := "Great"
	result := service.SentimentResult{Label: models.SentimentPositive, Score: 1}

	t.Run("record gone before write is a benign skip", func(t *testing.T) {
		svc := &mockSentimentWorkerService{record: sentimentTextRecord(&text), setErr: huberrors.ErrNotFound}
		metrics := newCountingSentimentMetrics()
		worker := NewFeedbackSentimentWorker(svc, stubSentimentSettings{}, &stubSentimentClient{result: result}, metrics)

		if err := worker.Work(context.Background(), sentimentJob(1)); err != nil {
			t.Fatalf("Work() error = %v, want nil", err)
		}

		if metrics.outcomes["skipped"] != 1 {
			t.Fatalf("skipped = %d, want 1", metrics.outcomes["skipped"])
		}
	})

	t.Run("content-superseded write is a benign skip", func(t *testing.T) {
		svc := &mockSentimentWorkerService{record: sentimentTextRecord(&text), setErr: huberrors.ErrClassificationSuperseded}
		metrics := newCountingSentimentMetrics()
		worker := NewFeedbackSentimentWorker(svc, stubSentimentSettings{}, &stubSentimentClient{result: result}, metrics)

		if err := worker.Work(context.Background(), sentimentJob(1)); err != nil {
			t.Fatalf("Work() error = %v, want nil (superseded is a skip, not a failure)", err)
		}

		if metrics.outcomes["skipped"] != 1 || metrics.workerErr["superseded"] != 1 {
			t.Fatalf("skipped=%d superseded=%d, want 1/1",
				metrics.outcomes["skipped"], metrics.workerErr["superseded"])
		}
	})

	t.Run("tenant write conflict retries", func(t *testing.T) {
		svc := &mockSentimentWorkerService{record: sentimentTextRecord(&text), setErr: huberrors.ErrTenantWriteConflict}
		metrics := newCountingSentimentMetrics()
		worker := NewFeedbackSentimentWorker(svc, stubSentimentSettings{}, &stubSentimentClient{result: result}, metrics)

		if err := worker.Work(context.Background(), sentimentJob(1)); err == nil {
			t.Fatal("Work() error = nil, want a retryable error")
		}

		if metrics.workerErr["tenant_write_conflict"] != 1 || metrics.outcomes["retry"] != 1 {
			t.Fatalf("tenant_write_conflict=%d retry=%d, want 1/1",
				metrics.workerErr["tenant_write_conflict"], metrics.outcomes["retry"])
		}
	})

	t.Run("other write error retries, failing on the final attempt", func(t *testing.T) {
		svc := &mockSentimentWorkerService{record: sentimentTextRecord(&text), setErr: errors.New("db unavailable")}
		metrics := newCountingSentimentMetrics()
		worker := NewFeedbackSentimentWorker(svc, stubSentimentSettings{}, &stubSentimentClient{result: result}, metrics)

		if err := worker.Work(context.Background(), sentimentJob(1)); err == nil {
			t.Fatal("Work() error = nil, want a failure")
		}

		if metrics.workerErr["update_failed"] != 1 || metrics.outcomes["retry"] != 1 || metrics.outcomes["failed_final"] != 0 {
			t.Fatalf("update_failed=%d retry=%d failed_final=%d, want 1/1/0 (transient write blip must not read as final)",
				metrics.workerErr["update_failed"], metrics.outcomes["retry"], metrics.outcomes["failed_final"])
		}

		if err := worker.Work(context.Background(), sentimentJob(3)); err == nil {
			t.Fatal("Work() error = nil, want a failure on the final attempt")
		}

		if metrics.outcomes["failed_final"] != 1 {
			t.Fatalf("failed_final=%d after the final attempt, want 1", metrics.outcomes["failed_final"])
		}
	})
}

func TestFeedbackSentimentWorker_DisabledForTenantSkips(t *testing.T) {
	// The enqueue provider fails open on a settings-read error, so the worker is the authoritative
	// gate: a tenant that turned sentiment off is skipped without classifying or writing.
	text := "Great product"
	off := false
	svc := &mockSentimentWorkerService{record: sentimentTextRecord(&text)}
	client := &stubSentimentClient{result: service.SentimentResult{Label: models.SentimentPositive, Score: 1}}
	metrics := newCountingSentimentMetrics()
	worker := NewFeedbackSentimentWorker(svc, stubSentimentSettings{enabled: &off}, client, metrics)

	if err := worker.Work(context.Background(), sentimentJob(1)); err != nil {
		t.Fatalf("Work() error = %v, want nil (a disabled tenant is a benign skip)", err)
	}

	if client.calls != 0 || len(svc.setCalls) != 0 {
		t.Fatalf("disabled tenant must not classify or write: calls=%d sets=%d", client.calls, len(svc.setCalls))
	}

	if metrics.outcomes["skipped"] != 1 {
		t.Fatalf("skipped outcomes = %d, want 1", metrics.outcomes["skipped"])
	}
}

func TestFeedbackSentimentWorker_SettingsReadErrorRetriesThenFailsFinal(t *testing.T) {
	// A settings-read failure is transient: the worker retries while attempts remain (so a
	// fail-open enqueue is not lost) and only fails final on the last attempt. It must not classify
	// against an unknown gate state.
	text := "Great product"

	for _, testCase := range []struct {
		name        string
		attempt     int
		wantOutcome string
		otherZero   string
	}{
		{"retry while attempts remain", 1, "retry", "failed_final"},
		{"final failure on last attempt", 3, "failed_final", "retry"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			svc := &mockSentimentWorkerService{record: sentimentTextRecord(&text)}
			client := &stubSentimentClient{result: service.SentimentResult{Label: models.SentimentPositive, Score: 1}}
			metrics := newCountingSentimentMetrics()
			worker := NewFeedbackSentimentWorker(
				svc, stubSentimentSettings{err: errors.New("db unavailable")}, client, metrics)

			if err := worker.Work(context.Background(), sentimentJob(testCase.attempt)); err == nil {
				t.Fatal("Work() error = nil, want a settings-read failure")
			}

			if client.calls != 0 || len(svc.setCalls) != 0 {
				t.Fatalf("unresolved settings must not classify or write: calls=%d sets=%d", client.calls, len(svc.setCalls))
			}

			if metrics.workerErr["settings_read_failed"] != 1 {
				t.Fatalf("settings_read_failed = %d, want 1", metrics.workerErr["settings_read_failed"])
			}

			if metrics.outcomes[testCase.wantOutcome] != 1 || metrics.outcomes[testCase.otherZero] != 0 {
				t.Fatalf("%s=%d %s=%d, want 1/0",
					testCase.wantOutcome, metrics.outcomes[testCase.wantOutcome],
					testCase.otherZero, metrics.outcomes[testCase.otherZero])
			}
		})
	}
}
