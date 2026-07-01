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
)

// These tests drive the generic EnrichmentWorker directly through a synthetic [A, R] config, so the
// shared Work/error-mapping branches are locked independently of which concrete enrichment happens
// to exercise each one (e.g. the supersession branch that no migrated type reaches yet).

type fakeEnrichmentArgs struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id"`
}

func (fakeEnrichmentArgs) Kind() string { return "fake_enrichment" }

type countingWorkerMetrics struct {
	outcomes  map[string]int
	workerErr map[string]int
}

func newCountingWorkerMetrics() *countingWorkerMetrics {
	return &countingWorkerMetrics{outcomes: map[string]int{}, workerErr: map[string]int{}}
}

func (m *countingWorkerMetrics) hooks() enrichmentWorkerMetrics {
	return enrichmentWorkerMetrics{
		outcome:     func(_ context.Context, status string) { m.outcomes[status]++ },
		duration:    func(_ context.Context, _ time.Duration, _ string) {},
		workerError: func(_ context.Context, reason string) { m.workerErr[reason]++ },
	}
}

// baseFakeConfig is a valid config whose default behavior is a successful classify + persist of a
// text record; each test overrides the one hook it exercises.
func baseFakeConfig(m *countingWorkerMetrics) enrichmentWorkerConfig[fakeEnrichmentArgs, string] {
	text := "content"

	return enrichmentWorkerConfig[fakeEnrichmentArgs, string]{
		name:     "fake",
		timeout:  time.Second,
		recordID: func(a fakeEnrichmentArgs) uuid.UUID { return a.FeedbackRecordID },
		getRecord: func(context.Context, uuid.UUID) (*models.FeedbackRecord, error) {
			return &models.FeedbackRecord{FieldType: models.FieldTypeText, ValueText: &text}, nil
		},
		hasContent:     func(*models.FeedbackRecord) bool { return true },
		classify:       func(context.Context, *models.FeedbackRecord) (string, error) { return "result", nil },
		persist:        func(context.Context, uuid.UUID, *string) error { return nil },
		rateLimited:    true,
		apiErrorReason: "fake_api_failed",
		metrics:        m.hooks(),
	}
}

func fakeJob(attempt int) *river.Job[fakeEnrichmentArgs] {
	return &river.Job[fakeEnrichmentArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: 3},
		Args:   fakeEnrichmentArgs{FeedbackRecordID: uuid.Must(uuid.NewV7())},
	}
}

func TestEnrichmentWorker_PersistSupersededIsSkipped(t *testing.T) {
	errSuperseded := errors.New("superseded")
	metrics := newCountingWorkerMetrics()
	cfg := baseFakeConfig(metrics)
	cfg.isSuperseded = func(err error) bool { return errors.Is(err, errSuperseded) }
	cfg.persist = func(context.Context, uuid.UUID, *string) error { return errSuperseded }

	if err := newEnrichmentWorker(cfg).Work(context.Background(), fakeJob(1)); err != nil {
		t.Fatalf("Work() = %v, want nil (a superseded write is a benign skip)", err)
	}

	if metrics.outcomes["skipped"] != 1 || metrics.outcomes["failed_final"] != 0 {
		t.Fatalf("skipped=%d failed_final=%d, want 1/0", metrics.outcomes["skipped"], metrics.outcomes["failed_final"])
	}
}

func TestEnrichmentWorker_TenantConflictFinalAttemptFails(t *testing.T) {
	metrics := newCountingWorkerMetrics()
	cfg := baseFakeConfig(metrics)
	cfg.persist = func(context.Context, uuid.UUID, *string) error { return huberrors.ErrTenantWriteConflict }

	if err := newEnrichmentWorker(cfg).Work(context.Background(), fakeJob(3)); err == nil {
		t.Fatal("Work() = nil, want a failure on the final attempt")
	}

	if metrics.workerErr["tenant_write_conflict"] != 1 || metrics.outcomes["failed_final"] != 1 {
		t.Fatalf("tenant_write_conflict=%d failed_final=%d, want 1/1",
			metrics.workerErr["tenant_write_conflict"], metrics.outcomes["failed_final"])
	}
}

func TestEnrichmentWorker_ClassifyNonFinalRetries(t *testing.T) {
	metrics := newCountingWorkerMetrics()
	cfg := baseFakeConfig(metrics)
	cfg.classify = func(context.Context, *models.FeedbackRecord) (string, error) {
		return "", errors.New("provider hiccup")
	}

	if err := newEnrichmentWorker(cfg).Work(context.Background(), fakeJob(1)); err == nil {
		t.Fatal("Work() = nil, want a retryable error on a non-final attempt")
	}

	if metrics.outcomes["retry"] != 1 || metrics.outcomes["failed_final"] != 0 || metrics.workerErr["fake_api_failed"] != 1 {
		t.Fatalf("retry=%d failed_final=%d fake_api_failed=%d, want 1/0/1",
			metrics.outcomes["retry"], metrics.outcomes["failed_final"], metrics.workerErr["fake_api_failed"])
	}
}

func TestEnrichmentWorker_ClearPathPersistErrorFails(t *testing.T) {
	metrics := newCountingWorkerMetrics()
	cfg := baseFakeConfig(metrics)
	cfg.hasContent = func(*models.FeedbackRecord) bool { return false } // force the clear path
	cfg.persist = func(context.Context, uuid.UUID, *string) error { return errors.New("db down") }

	if err := newEnrichmentWorker(cfg).Work(context.Background(), fakeJob(1)); err == nil {
		t.Fatal("Work() = nil, want a failure when the clear write fails")
	}

	if metrics.workerErr["update_failed"] != 1 || metrics.outcomes["failed_final"] != 1 {
		t.Fatalf("update_failed=%d failed_final=%d, want 1/1",
			metrics.workerErr["update_failed"], metrics.outcomes["failed_final"])
	}
}

func TestNewEnrichmentWorker_PanicsOnMissingHook(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("newEnrichmentWorker did not panic on a config missing a required hook")
		}
	}()

	cfg := baseFakeConfig(newCountingWorkerMetrics())
	cfg.classify = nil // omit a required hook

	newEnrichmentWorker(cfg)
}
