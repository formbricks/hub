package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type mockFeedbackRecordsRepo struct {
	record                     *models.FeedbackRecord
	createReq                  *models.CreateFeedbackRecordRequest
	deleteByUserGroups         []models.DeletedFeedbackRecordsByTenant
	deletedID                  uuid.UUID
	deleteByUserFilters        *models.DeleteFeedbackRecordsByUserFilters
	translationBackfillTargets []models.TranslationBackfillTarget
	translationBackfillErr     error
	tenantBackfillTargets      []models.TranslationBackfillTarget
	tenantBackfillErr          error
}

func (m *mockFeedbackRecordsRepo) Create(
	_ context.Context, req *models.CreateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	reqCopy := *req
	m.createReq = &reqCopy

	if m.record != nil {
		return m.record, nil
	}

	return &models.FeedbackRecord{TenantID: req.TenantID}, nil
}

func (m *mockFeedbackRecordsRepo) GetByID(_ context.Context, _ uuid.UUID) (*models.FeedbackRecord, error) {
	return m.record, nil
}

func (m *mockFeedbackRecordsRepo) List(
	_ context.Context, _ *models.ListFeedbackRecordsFilters,
) ([]models.FeedbackRecord, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (m *mockFeedbackRecordsRepo) ListAfterCursor(
	_ context.Context, _ *models.ListFeedbackRecordsFilters, _ time.Time, _ uuid.UUID,
) ([]models.FeedbackRecord, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (m *mockFeedbackRecordsRepo) Update(
	_ context.Context, _ uuid.UUID, _ *models.UpdateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	return nil, errors.New("not implemented")
}

func (m *mockFeedbackRecordsRepo) SetTranslation(
	_ context.Context, _ uuid.UUID, _ *string, _, _ string,
) error {
	return nil
}

func (m *mockFeedbackRecordsRepo) ListTranslationBackfillTargets(
	_ context.Context, afterID uuid.UUID, _ int, _ string,
) ([]models.TranslationBackfillTarget, error) {
	if m.translationBackfillErr != nil {
		return nil, m.translationBackfillErr
	}

	// First page (afterID == Nil) returns the configured set; later pages are empty.
	if afterID != uuid.Nil {
		return nil, nil
	}

	return m.translationBackfillTargets, nil
}

func (m *mockFeedbackRecordsRepo) ListTranslationBackfillTargetsForTenant(
	_ context.Context, _ string, afterID uuid.UUID, _ int, _ string,
) ([]models.TranslationBackfillTarget, error) {
	if m.tenantBackfillErr != nil {
		return nil, m.tenantBackfillErr
	}

	// First page (afterID == Nil) returns the configured set; later pages are empty, so
	// the keyset loop terminates after one short page.
	if afterID != uuid.Nil {
		return nil, nil
	}

	return m.tenantBackfillTargets, nil
}

func (m *mockFeedbackRecordsRepo) Delete(_ context.Context, id uuid.UUID) error {
	m.deletedID = id

	return nil
}

func (m *mockFeedbackRecordsRepo) DeleteByUser(
	_ context.Context, filters *models.DeleteFeedbackRecordsByUserFilters,
) ([]models.DeletedFeedbackRecordsByTenant, error) {
	m.deleteByUserFilters = filters

	return m.deleteByUserGroups, nil
}

func TestFeedbackRecordsService_DeleteFeedbackRecord_PublishesTenantAwareDeletedEvent(t *testing.T) {
	ctx := context.Background()
	recordID := uuid.Must(uuid.NewV7())
	tenantID := "org-123"
	repo := &mockFeedbackRecordsRepo{record: &models.FeedbackRecord{ID: recordID, TenantID: tenantID}}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	err := svc.DeleteFeedbackRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("DeleteFeedbackRecord() error = %v", err)
	}

	if repo.deletedID != recordID {
		t.Fatalf("deletedID = %v, want %v", repo.deletedID, recordID)
	}

	assertDeletedEventData(t, publisher, datatypes.FeedbackRecordDeleted, tenantID, []uuid.UUID{recordID})
}

func TestFeedbackRecordsService_CreateFeedbackRecord_NormalizesTenantID(t *testing.T) {
	ctx := context.Background()
	inputTenantID := " org-123 "
	repo := &mockFeedbackRecordsRepo{}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	record, err := svc.CreateFeedbackRecord(ctx, &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "field-1",
		FieldType:    models.FieldTypeText,
		TenantID:     inputTenantID,
		SubmissionID: "submission-1",
	})
	if err != nil {
		t.Fatalf("CreateFeedbackRecord() error = %v", err)
	}

	if repo.createReq == nil {
		t.Fatal("repo Create request = nil")
	}

	if repo.createReq.TenantID != "org-123" {
		t.Fatalf("repo TenantID = %q, want org-123", repo.createReq.TenantID)
	}

	if record.TenantID != "org-123" {
		t.Fatalf("record TenantID = %q, want org-123", record.TenantID)
	}

	if publisher.callCount != 1 || publisher.eventType != datatypes.FeedbackRecordCreated {
		t.Fatalf("published event = (%d, %s), want one feedback_record.created", publisher.callCount, publisher.eventType)
	}
}

func TestFeedbackRecordsService_DeleteFeedbackRecordsByUser_PublishesTenantAwareDeletedEventsByTenant(t *testing.T) {
	ctx := context.Background()
	tenantA := "org-123"
	tenantB := "org-456"
	tenantAIDs := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	tenantBIDs := []uuid.UUID{uuid.Must(uuid.NewV7())}
	repo := &mockFeedbackRecordsRepo{
		deleteByUserGroups: []models.DeletedFeedbackRecordsByTenant{
			{TenantID: tenantA, IDs: tenantAIDs},
			{TenantID: tenantB, IDs: tenantBIDs},
		},
	}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	count, err := svc.DeleteFeedbackRecordsByUser(ctx, &models.DeleteFeedbackRecordsByUserFilters{UserID: " user-123 "})
	if err != nil {
		t.Fatalf("DeleteFeedbackRecordsByUser() error = %v", err)
	}

	if repo.deleteByUserFilters == nil {
		t.Fatal("repo DeleteByUser filters = nil")
	}

	if repo.deleteByUserFilters.UserID != "user-123" {
		t.Fatalf("repo UserID = %q, want user-123", repo.deleteByUserFilters.UserID)
	}

	if repo.deleteByUserFilters.TenantID != nil {
		t.Fatalf("repo TenantID = %q, want nil for all-tenant delete", *repo.deleteByUserFilters.TenantID)
	}

	if count != len(tenantAIDs)+len(tenantBIDs) {
		t.Fatalf("count = %d, want %d", count, len(tenantAIDs)+len(tenantBIDs))
	}

	assertDeletedEventDataAt(t, publisher, 0, datatypes.FeedbackRecordDeleted, tenantA, tenantAIDs)
	assertDeletedEventDataAt(t, publisher, 1, datatypes.FeedbackRecordDeleted, tenantB, tenantBIDs)
}

func TestFeedbackRecordsService_DeleteFeedbackRecordsByUser_NormalizesTenantFilter(t *testing.T) {
	ctx := context.Background()
	tenantID := " org-123 "
	deletedID := uuid.Must(uuid.NewV7())
	repo := &mockFeedbackRecordsRepo{
		deleteByUserGroups: []models.DeletedFeedbackRecordsByTenant{
			{TenantID: "org-123", IDs: []uuid.UUID{deletedID}},
		},
	}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	count, err := svc.DeleteFeedbackRecordsByUser(ctx, &models.DeleteFeedbackRecordsByUserFilters{
		UserID:   "user-123",
		TenantID: &tenantID,
	})
	if err != nil {
		t.Fatalf("DeleteFeedbackRecordsByUser() error = %v", err)
	}

	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	if repo.deleteByUserFilters == nil || repo.deleteByUserFilters.TenantID == nil {
		t.Fatal("repo TenantID = nil, want normalized tenant")
	}

	if *repo.deleteByUserFilters.TenantID != "org-123" {
		t.Fatalf("repo TenantID = %q, want org-123", *repo.deleteByUserFilters.TenantID)
	}

	assertDeletedEventData(t, publisher, datatypes.FeedbackRecordDeleted, "org-123", []uuid.UUID{deletedID})
}

func TestFeedbackRecordsService_DeleteFeedbackRecordsByUser_RejectsOverlengthTenantFilter(t *testing.T) {
	ctx := context.Background()
	tenantID := strings.Repeat("a", maxTenantIDLength+1)
	repo := &mockFeedbackRecordsRepo{}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	count, err := svc.DeleteFeedbackRecordsByUser(ctx, &models.DeleteFeedbackRecordsByUserFilters{
		UserID:   "user-123",
		TenantID: &tenantID,
	})
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("DeleteFeedbackRecordsByUser() error = %v, want validation error", err)
	}

	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}

	if repo.deleteByUserFilters != nil {
		t.Fatal("repo DeleteByUser was called, want validation before repository")
	}

	if publisher.callCount != 0 {
		t.Fatalf("published %d events, want 0", publisher.callCount)
	}
}

func TestFeedbackRecordsService_DeleteFeedbackRecordsByUser_RejectsOverlengthUserID(t *testing.T) {
	ctx := context.Background()
	userID := strings.Repeat("a", maxUserIDLength+1)
	repo := &mockFeedbackRecordsRepo{}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	count, err := svc.DeleteFeedbackRecordsByUser(ctx, &models.DeleteFeedbackRecordsByUserFilters{UserID: userID})
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("DeleteFeedbackRecordsByUser() error = %v, want validation error", err)
	}

	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}

	if repo.deleteByUserFilters != nil {
		t.Fatal("repo DeleteByUser was called, want validation before repository")
	}

	if publisher.callCount != 0 {
		t.Fatalf("published %d events, want 0", publisher.callCount)
	}
}

func TestFeedbackRecordsService_DeleteFeedbackRecordsByUser_RequiresUserID(t *testing.T) {
	ctx := context.Background()
	repo := &mockFeedbackRecordsRepo{
		deleteByUserGroups: []models.DeletedFeedbackRecordsByTenant{
			{TenantID: "org-123", IDs: []uuid.UUID{uuid.Must(uuid.NewV7())}},
		},
	}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	count, err := svc.DeleteFeedbackRecordsByUser(ctx, &models.DeleteFeedbackRecordsByUserFilters{})
	if !errors.Is(err, ErrUserIDRequired) {
		t.Fatalf("DeleteFeedbackRecordsByUser() error = %v, want ErrUserIDRequired", err)
	}

	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}

	if publisher.callCount != 0 {
		t.Fatalf("published %d events, want 0", publisher.callCount)
	}
}

func TestFeedbackRecordsService_SetTranslation_RequiresLangKey(t *testing.T) {
	svc := NewFeedbackRecordsService(&mockFeedbackRecordsRepo{}, nil, "", nil, nil, "", 0, "")
	translated := "Hallo"

	// A translation with a blank lang key is rejected before reaching the repo.
	if err := svc.SetTranslation(context.Background(), uuid.New(), &translated, "  "); !errors.Is(err, ErrTranslationLangKeyRequired) {
		t.Fatalf("err = %v, want ErrTranslationLangKeyRequired", err)
	}

	// Clearing (nil translation) with an empty key is allowed.
	if err := svc.SetTranslation(context.Background(), uuid.New(), nil, ""); err != nil {
		t.Fatalf("clear err = %v, want nil", err)
	}
}

func TestFeedbackRecordsService_BackfillTranslationsForTenant(t *testing.T) {
	firstID := uuid.New()
	secondID := uuid.New()
	repo := &mockFeedbackRecordsRepo{
		tenantBackfillTargets: []models.TranslationBackfillTarget{
			{FeedbackRecordID: firstID, TargetLang: "de-DE"},
			{FeedbackRecordID: secondID, TargetLang: "de-DE"},
		},
	}
	inserter := &mockTranslationInserter{}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	enqueued, err := svc.BackfillTranslationsForTenant(context.Background(), inserter, TranslationsQueueName, 3, "org-1")
	if err != nil {
		t.Fatalf("BackfillTranslationsForTenant() error = %v", err)
	}

	if enqueued != 2 || len(inserter.calls) != 2 {
		t.Fatalf("enqueued=%d calls=%d, want 2/2", enqueued, len(inserter.calls))
	}

	if inserter.calls[0].FeedbackRecordID != firstID ||
		inserter.calls[0].TargetLang != "de-DE" || inserter.calls[0].ValueTextHash != "backfill" {
		t.Fatalf("first job = %+v, want firstID/de-DE/backfill", inserter.calls[0])
	}
}

func TestFeedbackRecordsService_BackfillTranslationsForTenant_RepoError(t *testing.T) {
	repo := &mockFeedbackRecordsRepo{tenantBackfillErr: errors.New("db down")}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	_, err := svc.BackfillTranslationsForTenant(
		context.Background(), &mockTranslationInserter{}, TranslationsQueueName, 3, "org-1")
	if err == nil {
		t.Fatal("BackfillTranslationsForTenant() = nil error, want repo error")
	}
}

func TestFeedbackRecordsService_BackfillTranslations(t *testing.T) {
	id1 := uuid.Must(uuid.NewV7())
	id2 := uuid.Must(uuid.NewV7())
	repo := &mockFeedbackRecordsRepo{
		translationBackfillTargets: []models.TranslationBackfillTarget{
			{FeedbackRecordID: id1, TargetLang: "de-DE"},
			{FeedbackRecordID: id2, TargetLang: "fr-FR"},
		},
	}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")
	inserter := &mockTranslationInserter{}

	enqueued, err := svc.BackfillTranslations(context.Background(), inserter, TranslationsQueueName, 3)
	if err != nil {
		t.Fatalf("BackfillTranslations() error = %v", err)
	}

	if enqueued != 2 || len(inserter.calls) != 2 {
		t.Fatalf("enqueued = %d, inserter calls = %d, want 2 and 2", enqueued, len(inserter.calls))
	}

	first := inserter.calls[0]
	if first.FeedbackRecordID != id1 || first.TargetLang != "de-DE" || first.ValueTextHash != "backfill" {
		t.Fatalf("first job = %+v, want {%s de-DE backfill}", first, id1)
	}
}

func TestFeedbackRecordsService_BackfillTranslations_RepoError(t *testing.T) {
	repo := &mockFeedbackRecordsRepo{translationBackfillErr: errors.New("boom")}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	_, err := svc.BackfillTranslations(context.Background(), &mockTranslationInserter{}, TranslationsQueueName, 3)
	if err == nil {
		t.Fatal("BackfillTranslations() = nil, want the repo error propagated")
	}
}

func assertDeletedEventDataAt(
	t *testing.T,
	publisher *capturePublisher,
	index int,
	eventType datatypes.EventType,
	tenantID string,
	ids []uuid.UUID,
) {
	t.Helper()

	if publisher.callCount <= index {
		t.Fatalf("published %d events, want event at index %d", publisher.callCount, index)
	}

	event := publisher.events[index]
	if event.eventType != eventType {
		t.Fatalf("published event type = %s, want %s", event.eventType, eventType)
	}

	data, ok := event.data.(models.DeletedIDsEventData)
	if !ok {
		t.Fatalf("published data type = %T, want DeletedIDsEventData", event.data)
	}

	if data.TenantID != tenantID {
		t.Errorf("TenantID = %q, want %q", data.TenantID, tenantID)
	}

	if len(data.IDs) != len(ids) {
		t.Fatalf("IDs length = %d, want %d", len(data.IDs), len(ids))
	}

	for i := range ids {
		if data.IDs[i] != ids[i] {
			t.Errorf("IDs[%d] = %v, want %v", i, data.IDs[i], ids[i])
		}
	}
}

func assertDeletedEventData(
	t *testing.T, publisher *capturePublisher, eventType datatypes.EventType, tenantID string, ids []uuid.UUID,
) {
	t.Helper()

	if publisher.callCount != 1 || publisher.eventType != eventType {
		t.Fatalf("published event = (%d, %s), want one %s", publisher.callCount, publisher.eventType, eventType)
	}

	data, ok := publisher.data.(models.DeletedIDsEventData)
	if !ok {
		t.Fatalf("published data type = %T, want DeletedIDsEventData", publisher.data)
	}

	if data.TenantID != tenantID {
		t.Errorf("TenantID = %q, want %q", data.TenantID, tenantID)
	}

	if len(data.IDs) != len(ids) {
		t.Fatalf("IDs length = %d, want %d", len(data.IDs), len(ids))
	}

	for i := range ids {
		if data.IDs[i] != ids[i] {
			t.Errorf("IDs[%d] = %v, want %v", i, data.IDs[i], ids[i])
		}
	}
}
