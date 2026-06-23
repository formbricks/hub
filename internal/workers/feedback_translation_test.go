package workers

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

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
	worker := NewFeedbackTranslationWorker(svc, client)

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
	worker := NewFeedbackTranslationWorker(svc, client)

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

func TestFeedbackTranslationWorker_SkipsWhenValueTextEmpty(t *testing.T) {
	svc := &mockTranslationWorkerService{record: translationRecord("   ", "fr")}
	client := &stubTranslationClient{out: "x"}
	worker := NewFeedbackTranslationWorker(svc, client)

	if err := worker.Work(context.Background(), translationJob("en-US", 1)); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if len(client.calls) != 0 || len(svc.setCalls) != 0 {
		t.Fatal("expected no translate/set for empty value_text")
	}
}

func TestFeedbackTranslationWorker_NotFoundCompletes(t *testing.T) {
	svc := &mockTranslationWorkerService{getErr: huberrors.NewNotFoundError("feedback record", "gone")}
	worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{})

	if err := worker.Work(context.Background(), translationJob("en-US", 1)); err != nil {
		t.Fatalf("Work() error = %v, want nil (not-found completes)", err)
	}
}

func TestFeedbackTranslationWorker_ProviderErrorRetriesThenFails(t *testing.T) {
	svc := &mockTranslationWorkerService{record: translationRecord("Bonjour", "fr")}
	client := &stubTranslationClient{err: errors.New("api down")}
	worker := NewFeedbackTranslationWorker(svc, client)

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
	worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{out: "Hi"})

	if err := worker.Work(context.Background(), translationJob("en-US", 1)); err == nil {
		t.Fatal("Work() = nil, want retry on tenant write conflict")
	}
}

func TestFeedbackTranslationWorker_RecordGoneOnWriteCompletes(t *testing.T) {
	svc := &mockTranslationWorkerService{
		record: translationRecord("Bonjour", "fr"),
		setErr: huberrors.NewNotFoundError("feedback record", "gone"),
	}
	worker := NewFeedbackTranslationWorker(svc, &stubTranslationClient{out: "Hi"})

	if err := worker.Work(context.Background(), translationJob("en-US", 1)); err != nil {
		t.Fatalf("Work() error = %v, want nil (record gone before write completes)", err)
	}
}
