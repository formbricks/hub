package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
)

type mockFeedbackRecordsRepo struct {
	record            *models.FeedbackRecord
	createReq         *models.CreateFeedbackRecordRequest
	bulkGroups        []models.DeletedFeedbackRecordsByTenant
	deletedID         uuid.UUID
	bulkDeleteFilters *models.BulkDeleteFilters
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

func (m *mockFeedbackRecordsRepo) Delete(_ context.Context, id uuid.UUID) error {
	m.deletedID = id

	return nil
}

func (m *mockFeedbackRecordsRepo) BulkDelete(
	_ context.Context, filters *models.BulkDeleteFilters,
) ([]models.DeletedFeedbackRecordsByTenant, error) {
	m.bulkDeleteFilters = filters

	return m.bulkGroups, nil
}

func TestFeedbackRecordsService_DeleteFeedbackRecord_PublishesTenantAwareDeletedEvent(t *testing.T) {
	ctx := context.Background()
	recordID := uuid.Must(uuid.NewV7())
	tenantID := "org-123"
	repo := &mockFeedbackRecordsRepo{record: &models.FeedbackRecord{ID: recordID, TenantID: tenantID}}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0)

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
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0)

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

func TestFeedbackRecordsService_BulkDeleteFeedbackRecords_PublishesTenantAwareDeletedEventsByTenant(t *testing.T) {
	ctx := context.Background()
	tenantA := "org-123"
	tenantB := "org-456"
	tenantAIDs := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	tenantBIDs := []uuid.UUID{uuid.Must(uuid.NewV7())}
	repo := &mockFeedbackRecordsRepo{
		bulkGroups: []models.DeletedFeedbackRecordsByTenant{
			{TenantID: tenantA, IDs: tenantAIDs},
			{TenantID: tenantB, IDs: tenantBIDs},
		},
	}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0)

	count, err := svc.BulkDeleteFeedbackRecords(ctx, &models.BulkDeleteFilters{UserID: "user-123"})
	if err != nil {
		t.Fatalf("BulkDeleteFeedbackRecords() error = %v", err)
	}

	if repo.bulkDeleteFilters == nil {
		t.Fatal("repo BulkDelete filters = nil")
	}

	if repo.bulkDeleteFilters.UserID != "user-123" {
		t.Fatalf("repo UserID = %q, want user-123", repo.bulkDeleteFilters.UserID)
	}

	if repo.bulkDeleteFilters.TenantID != nil {
		t.Fatalf("repo TenantID = %q, want nil for all-tenant delete", *repo.bulkDeleteFilters.TenantID)
	}

	if count != len(tenantAIDs)+len(tenantBIDs) {
		t.Fatalf("count = %d, want %d", count, len(tenantAIDs)+len(tenantBIDs))
	}

	assertDeletedEventDataAt(t, publisher, 0, datatypes.FeedbackRecordDeleted, tenantA, tenantAIDs)
	assertDeletedEventDataAt(t, publisher, 1, datatypes.FeedbackRecordDeleted, tenantB, tenantBIDs)
}

func TestFeedbackRecordsService_BulkDeleteFeedbackRecords_NormalizesTenantFilter(t *testing.T) {
	ctx := context.Background()
	tenantID := " org-123 "
	deletedID := uuid.Must(uuid.NewV7())
	repo := &mockFeedbackRecordsRepo{
		bulkGroups: []models.DeletedFeedbackRecordsByTenant{
			{TenantID: "org-123", IDs: []uuid.UUID{deletedID}},
		},
	}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0)

	count, err := svc.BulkDeleteFeedbackRecords(ctx, &models.BulkDeleteFilters{
		UserID:   "user-123",
		TenantID: &tenantID,
	})
	if err != nil {
		t.Fatalf("BulkDeleteFeedbackRecords() error = %v", err)
	}

	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	if repo.bulkDeleteFilters == nil || repo.bulkDeleteFilters.TenantID == nil {
		t.Fatal("repo TenantID = nil, want normalized tenant")
	}

	if *repo.bulkDeleteFilters.TenantID != "org-123" {
		t.Fatalf("repo TenantID = %q, want org-123", *repo.bulkDeleteFilters.TenantID)
	}

	assertDeletedEventData(t, publisher, datatypes.FeedbackRecordDeleted, "org-123", []uuid.UUID{deletedID})
}

func TestFeedbackRecordsService_BulkDeleteFeedbackRecords_RequiresUserID(t *testing.T) {
	ctx := context.Background()
	repo := &mockFeedbackRecordsRepo{
		bulkGroups: []models.DeletedFeedbackRecordsByTenant{
			{TenantID: "org-123", IDs: []uuid.UUID{uuid.Must(uuid.NewV7())}},
		},
	}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0)

	count, err := svc.BulkDeleteFeedbackRecords(ctx, &models.BulkDeleteFilters{})
	if !errors.Is(err, ErrUserIDRequired) {
		t.Fatalf("BulkDeleteFeedbackRecords() error = %v, want ErrUserIDRequired", err)
	}

	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}

	if publisher.callCount != 0 {
		t.Fatalf("published %d events, want 0", publisher.callCount)
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
