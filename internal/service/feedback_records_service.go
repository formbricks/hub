// Package service implements business logic for feedback records.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
)

// ErrUserIdentifierRequired is returned when bulk delete is called without user_identifier.
var ErrUserIdentifierRequired = errors.New("user_identifier is required")

// FeedbackRecordsRepository defines the interface for feedback records data access.
type FeedbackRecordsRepository interface {
	Create(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	GetByID(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	List(ctx context.Context, filters *models.ListFeedbackRecordsFilters) ([]models.FeedbackRecord, error)
	Count(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (int64, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	Delete(ctx context.Context, id uuid.UUID) error
	BulkDelete(ctx context.Context, userIdentifier string, tenantID *string) ([]models.FeedbackRecord, error)
}

// FeedbackRecordsService handles business logic for feedback records.
type FeedbackRecordsService struct {
	repo      FeedbackRecordsRepository
	publisher MessagePublisher
}

// NewFeedbackRecordsService creates a new feedback records service.
func NewFeedbackRecordsService(repo FeedbackRecordsRepository, publisher MessagePublisher) *FeedbackRecordsService {
	return &FeedbackRecordsService{
		repo:      repo,
		publisher: publisher,
	}
}

// CreateFeedbackRecord creates a new feedback record.
func (s *FeedbackRecordsService) CreateFeedbackRecord(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	record, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create feedback record: %w", err)
	}

	s.publisher.PublishEvent(ctx, datatypes.FeedbackRecordCreated, *record)

	return record, nil
}

// GetFeedbackRecord retrieves a single feedback record by ID.
func (s *FeedbackRecordsService) GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error) {
	record, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get feedback record: %w", err)
	}

	return record, nil
}

// ListFeedbackRecords retrieves a list of feedback records with optional filters.
func (s *FeedbackRecordsService) ListFeedbackRecords(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (*models.ListFeedbackRecordsResponse, error) {
	// Set default limit if not provided (validation ensures it's within bounds if provided)
	if filters.Limit <= 0 {
		filters.Limit = 100 // Default limit
	}

	records, err := s.repo.List(ctx, filters)
	if err != nil {
		return nil, fmt.Errorf("list feedback records: %w", err)
	}

	total, err := s.repo.Count(ctx, filters)
	if err != nil {
		return nil, fmt.Errorf("count feedback records: %w", err)
	}

	return &models.ListFeedbackRecordsResponse{
		Data:   records,
		Total:  total,
		Limit:  filters.Limit,
		Offset: filters.Offset,
	}, nil
}

// UpdateFeedbackRecord updates an existing feedback record.
func (s *FeedbackRecordsService) UpdateFeedbackRecord(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	record, err := s.repo.Update(ctx, id, req)
	if err != nil {
		return nil, fmt.Errorf("update feedback record: %w", err)
	}

	s.publisher.PublishEventWithChangedFields(ctx, datatypes.FeedbackRecordUpdated, *record, s.getChangedFields(req))

	return record, nil
}

// getChangedFields extracts which fields were changed from the update request.
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

// DeleteFeedbackRecord deletes a feedback record by ID.
func (s *FeedbackRecordsService) DeleteFeedbackRecord(ctx context.Context, id uuid.UUID) error {
	record, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get feedback record for delete: %w", err)
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete feedback record: %w", err)
	}

	s.publisher.PublishEvent(ctx, datatypes.FeedbackRecordDeleted, *record)

	return nil
}

// BulkDeleteFeedbackRecords deletes all feedback records matching user_identifier and optional tenant_id.
// Publishes FeedbackRecordDeleted for each deleted record (same as single delete).
// The repository uses DELETE ... RETURNING so we get the deleted rows in one query.
func (s *FeedbackRecordsService) BulkDeleteFeedbackRecords(ctx context.Context, userIdentifier string, tenantID *string) (int, error) {
	if userIdentifier == "" {
		return 0, ErrUserIdentifierRequired
	}

	records, err := s.repo.BulkDelete(ctx, userIdentifier, tenantID)
	if err != nil {
		return 0, fmt.Errorf("bulk delete feedback records: %w", err)
	}

	for i := range records {
		s.publisher.PublishEvent(ctx, datatypes.FeedbackRecordDeleted, records[i])
	}

	return len(records), nil
}
