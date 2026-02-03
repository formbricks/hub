package service

import (
	"context"
	"fmt"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
)

// FeedbackRecordsRepository defines the interface for feedback records data access.
type FeedbackRecordsRepository interface {
	Create(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	GetByID(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	List(ctx context.Context, filters *models.ListFeedbackRecordsFilters) ([]models.FeedbackRecord, error)
	Count(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (int64, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	Delete(ctx context.Context, id uuid.UUID) error
	BulkDelete(ctx context.Context, userIdentifier string, tenantID *string) (int64, error)
}

// FeedbackRecordsService handles business logic for feedback records
type FeedbackRecordsService struct {
	repo      FeedbackRecordsRepository
	publisher MessagePublisher
}

// NewFeedbackRecordsService creates a new feedback records service
func NewFeedbackRecordsService(repo FeedbackRecordsRepository, publisher MessagePublisher) *FeedbackRecordsService {
	return &FeedbackRecordsService{
		repo:      repo,
		publisher: publisher,
	}
}

// CreateFeedbackRecord creates a new feedback record
func (s *FeedbackRecordsService) CreateFeedbackRecord(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	record, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	s.publisher.PublishEvent(ctx, Event{
		Type: datatypes.FeedbackRecordCreated,
		Data: *record,
	})

	return record, nil
}

// GetFeedbackRecord retrieves a single feedback record by ID
func (s *FeedbackRecordsService) GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error) {
	return s.repo.GetByID(ctx, id)
}

// ListFeedbackRecords retrieves a list of feedback records with optional filters
func (s *FeedbackRecordsService) ListFeedbackRecords(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (*models.ListFeedbackRecordsResponse, error) {
	// Set default limit if not provided (validation ensures it's within bounds if provided)
	if filters.Limit <= 0 {
		filters.Limit = 100 // Default limit
	}

	records, err := s.repo.List(ctx, filters)
	if err != nil {
		return nil, err
	}

	total, err := s.repo.Count(ctx, filters)
	if err != nil {
		return nil, err
	}

	return &models.ListFeedbackRecordsResponse{
		Data:   records,
		Total:  total,
		Limit:  filters.Limit,
		Offset: filters.Offset,
	}, nil
}

// UpdateFeedbackRecord updates an existing feedback record
func (s *FeedbackRecordsService) UpdateFeedbackRecord(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	changedFields := s.getChangedFields(req)

	record, err := s.repo.Update(ctx, id, req)
	if err != nil {
		return nil, err
	}

	s.publisher.PublishEvent(ctx, Event{
		Type:          datatypes.FeedbackRecordUpdated,
		Data:          *record,
		ChangedFields: changedFields,
	})

	return record, nil
}

// getChangedFields extracts which fields were changed from the update request
func (s *FeedbackRecordsService) getChangedFields(req *models.UpdateFeedbackRecordRequest) []string {
	var fields []string

	if req.ValueText != nil {
		fields = append(fields, "value_text")
	}
	if req.ValueNumber != nil {
		fields = append(fields, "value_number")
	}
	if req.ValueBoolean != nil {
		fields = append(fields, "value_boolean")
	}
	if req.ValueDate != nil {
		fields = append(fields, "value_date")
	}
	if req.Metadata != nil {
		fields = append(fields, "metadata")
	}
	if req.Language != nil {
		fields = append(fields, "language")
	}
	if req.UserIdentifier != nil {
		fields = append(fields, "user_identifier")
	}

	return fields
}

// DeleteFeedbackRecord deletes a feedback record by ID
func (s *FeedbackRecordsService) DeleteFeedbackRecord(ctx context.Context, id uuid.UUID) error {
	// Get record before deletion for event payload
	record, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	s.publisher.PublishEvent(ctx, Event{
		Type: datatypes.FeedbackRecordDeleted,
		Data: *record,
	})

	return nil
}

// BulkDeleteFeedbackRecords deletes all feedback records matching user_identifier and optional tenant_id
func (s *FeedbackRecordsService) BulkDeleteFeedbackRecords(ctx context.Context, userIdentifier string, tenantID *string) (int64, error) {
	if userIdentifier == "" {
		return 0, fmt.Errorf("user_identifier is required")
	}

	return s.repo.BulkDelete(ctx, userIdentifier, tenantID)
}
