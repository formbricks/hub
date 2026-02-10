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

// FeedbackRecordsRepository defines the interface for feedback records data access.
type FeedbackRecordsRepository interface {
	Create(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	GetByID(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	List(ctx context.Context, filters *models.ListFeedbackRecordsFilters) ([]models.FeedbackRecord, error)
	Count(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (int64, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	Delete(ctx context.Context, id uuid.UUID) error
	BulkDelete(ctx context.Context, userIdentifier string, tenantID *string) ([]uuid.UUID, error)
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

	s.publisher.PublishEventWithChangedFields(ctx, datatypes.FeedbackRecordUpdated, *record, req.ChangedFields())
	return record, nil
}

// DeleteFeedbackRecord deletes a feedback record by ID.
// Publishes FeedbackRecordDeleted with data = [id] (array of deleted IDs) for consistency with bulk delete.
func (s *FeedbackRecordsService) DeleteFeedbackRecord(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete feedback record: %w", err)
	}
	s.publisher.PublishEvent(ctx, datatypes.FeedbackRecordDeleted, []uuid.UUID{id})
	return nil
}

// BulkDeleteFeedbackRecords deletes all feedback records matching user_identifier and optional tenant_id.
// Publishes a single FeedbackRecordDeleted event with data = [id1, id2, ...] (array of deleted IDs).
func (s *FeedbackRecordsService) BulkDeleteFeedbackRecords(ctx context.Context, userIdentifier string, tenantID *string) (int, error) {
	if userIdentifier == "" {
		return 0, errors.New("user_identifier is required")
	}

	ids, err := s.repo.BulkDelete(ctx, userIdentifier, tenantID)
	if err != nil {
		return 0, fmt.Errorf("bulk delete feedback records: %w", err)
	}

	if len(ids) > 0 {
		s.publisher.PublishEvent(ctx, datatypes.FeedbackRecordDeleted, ids)
	}
	return len(ids), nil
}
