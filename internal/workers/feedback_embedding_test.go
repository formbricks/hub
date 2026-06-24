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

// countingEmbeddingMetrics records outcome/worker-error counts for assertions.
type countingEmbeddingMetrics struct {
	outcomes  map[string]int
	workerErr map[string]int
}

func newCountingEmbeddingMetrics() *countingEmbeddingMetrics {
	return &countingEmbeddingMetrics{outcomes: map[string]int{}, workerErr: map[string]int{}}
}

func (m *countingEmbeddingMetrics) RecordJobsEnqueued(context.Context, int64)   {}
func (m *countingEmbeddingMetrics) RecordProviderError(context.Context, string) {}

func (m *countingEmbeddingMetrics) RecordEmbeddingOutcome(_ context.Context, status string) {
	m.outcomes[status]++
}

func (m *countingEmbeddingMetrics) RecordWorkerError(_ context.Context, reason string) {
	m.workerErr[reason]++
}

func (m *countingEmbeddingMetrics) RecordEmbeddingDuration(context.Context, time.Duration, string) {}

var _ observability.EmbeddingMetrics = (*countingEmbeddingMetrics)(nil)

type mockEmbeddingService struct {
	record          *models.FeedbackRecord
	getErr          error
	setErr          error
	setCalls        int
	setEmbeddingNil bool
}

func (m *mockEmbeddingService) GetFeedbackRecord(_ context.Context, _ uuid.UUID) (*models.FeedbackRecord, error) {
	return m.record, m.getErr
}

func (m *mockEmbeddingService) SetEmbedding(
	_ context.Context, _ uuid.UUID, _ string, embedding []float32,
) error {
	m.setCalls++
	m.setEmbeddingNil = embedding == nil

	return m.setErr
}

type mockEmbeddingClient struct {
	embedding []float32
	err       error
}

func (m *mockEmbeddingClient) CreateEmbedding(_ context.Context, _ string) ([]float32, error) {
	return m.embedding, m.err
}

func (m *mockEmbeddingClient) CreateEmbeddingForQuery(_ context.Context, _ string) ([]float32, error) {
	return m.embedding, m.err
}

func embeddingJob() *river.Job[service.FeedbackEmbeddingArgs] {
	return &river.Job[service.FeedbackEmbeddingArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 3},
		Args: service.FeedbackEmbeddingArgs{
			FeedbackRecordID: uuid.Must(uuid.NewV7()),
			Model:            "test-model",
		},
	}
}

func textRecord(valueText string) *models.FeedbackRecord {
	label := "How was it?"

	record := &models.FeedbackRecord{FieldType: models.FieldTypeText, FieldLabel: &label}
	if valueText != "" {
		record.ValueText = &valueText
	}

	return record
}

func TestFeedbackEmbeddingWorker_GetNotFoundRecordsSkipped(t *testing.T) {
	metrics := newCountingEmbeddingMetrics()
	svc := &mockEmbeddingService{getErr: huberrors.NewNotFoundError("feedback record", "gone")}
	worker := NewFeedbackEmbeddingWorker(svc, &mockEmbeddingClient{}, "", metrics)

	if err := worker.Work(context.Background(), embeddingJob()); err != nil {
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

func TestFeedbackEmbeddingWorker_Work_SetEmbeddingConflict(t *testing.T) {
	ctx := context.Background()

	t.Run("tenant write conflict returns error so River retries", func(t *testing.T) {
		svc := &mockEmbeddingService{
			record: textRecord("great product"),
			setErr: huberrors.NewTenantWriteConflictError(""),
		}
		worker := NewFeedbackEmbeddingWorker(svc, &mockEmbeddingClient{embedding: []float32{0.1}}, "", nil)

		err := worker.Work(ctx, embeddingJob())
		if !errors.Is(err, huberrors.ErrTenantWriteConflict) {
			t.Fatalf("Work() error = %v, want tenant write conflict for retry", err)
		}
	})

	t.Run("record gone before write completes the job", func(t *testing.T) {
		svc := &mockEmbeddingService{
			record: textRecord("great product"),
			setErr: huberrors.NewNotFoundError("feedback record", ""),
		}
		worker := NewFeedbackEmbeddingWorker(svc, &mockEmbeddingClient{embedding: []float32{0.1}}, "", nil)

		err := worker.Work(ctx, embeddingJob())
		if err != nil {
			t.Fatalf("Work() error = %v, want nil (record purged, nothing to embed)", err)
		}

		if svc.setCalls != 1 {
			t.Fatalf("SetEmbedding called %d times, want 1", svc.setCalls)
		}
	})

	t.Run("other set errors still fail the job", func(t *testing.T) {
		svc := &mockEmbeddingService{
			record: textRecord("great product"),
			setErr: errors.New("connection lost"),
		}
		worker := NewFeedbackEmbeddingWorker(svc, &mockEmbeddingClient{embedding: []float32{0.1}}, "", nil)

		err := worker.Work(ctx, embeddingJob())
		if err == nil {
			t.Fatal("Work() error = nil, want error")
		}

		if errors.Is(err, huberrors.ErrTenantWriteConflict) {
			t.Fatalf("Work() error = %v, must not be a tenant write conflict", err)
		}
	})
}

func TestFeedbackEmbeddingWorker_Work_EmptyTextConflict(t *testing.T) {
	ctx := context.Background()

	t.Run("clear during purge returns error so River retries", func(t *testing.T) {
		svc := &mockEmbeddingService{
			record: textRecord(""),
			setErr: huberrors.NewTenantWriteConflictError(""),
		}
		worker := NewFeedbackEmbeddingWorker(svc, &mockEmbeddingClient{}, "", nil)

		err := worker.Work(ctx, embeddingJob())
		if !errors.Is(err, huberrors.ErrTenantWriteConflict) {
			t.Fatalf("Work() error = %v, want tenant write conflict for retry", err)
		}

		if !svc.setEmbeddingNil {
			t.Fatal("SetEmbedding should have been called with nil to clear the embedding")
		}
	})

	t.Run("record gone before clear completes the job", func(t *testing.T) {
		svc := &mockEmbeddingService{
			record: textRecord(""),
			setErr: huberrors.NewNotFoundError("feedback record", ""),
		}
		worker := NewFeedbackEmbeddingWorker(svc, &mockEmbeddingClient{}, "", nil)

		err := worker.Work(ctx, embeddingJob())
		if err != nil {
			t.Fatalf("Work() error = %v, want nil (record purged, nothing to clear)", err)
		}
	})
}
