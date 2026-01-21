package service

import (
	"context"
	"fmt"

	apperrors "github.com/formbricks/hub/internal/errors"
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
	Search(ctx context.Context, req *models.SearchFeedbackRecordsRequest) ([]models.FeedbackRecord, error)
}

// FeedbackRecordsService handles business logic for feedback records
type FeedbackRecordsService struct {
	repo FeedbackRecordsRepository
}

// NewFeedbackRecordsService creates a new feedback records service
func NewFeedbackRecordsService(repo FeedbackRecordsRepository) *FeedbackRecordsService {
	return &FeedbackRecordsService{repo: repo}
}

// CreateFeedbackRecord creates a new feedback record
func (s *FeedbackRecordsService) CreateFeedbackRecord(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	if err := s.validateCreateRequest(req); err != nil {
		return nil, err
	}

	return s.repo.Create(ctx, req)
}

// GetFeedbackRecord retrieves a single feedback record by ID
func (s *FeedbackRecordsService) GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error) {
	return s.repo.GetByID(ctx, id)
}

// ListFeedbackRecords retrieves a list of feedback records with optional filters
func (s *FeedbackRecordsService) ListFeedbackRecords(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (*models.ListFeedbackRecordsResponse, error) {
	if filters.Limit <= 0 {
		filters.Limit = 100 // Default limit
	}
	if filters.Limit > 1000 {
		filters.Limit = 1000 // Max limit
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
	if err := s.validateUpdateRequest(req); err != nil {
		return nil, err
	}

	return s.repo.Update(ctx, id, req)
}

// DeleteFeedbackRecord deletes a feedback record by ID
func (s *FeedbackRecordsService) DeleteFeedbackRecord(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

// SearchFeedbackRecords performs semantic search
func (s *FeedbackRecordsService) SearchFeedbackRecords(ctx context.Context, req *models.SearchFeedbackRecordsRequest) (*models.SearchFeedbackRecordsResponse, error) {
	// Set default limit and enforce max
	if req.Limit <= 0 {
		req.Limit = 10 // Default limit
	}
	if req.Limit > 100 {
		req.Limit = 100 // Max limit
	}

	// Call repository search
	records, err := s.repo.Search(ctx, req)
	if err != nil {
		return nil, err
	}

	// Convert to SearchResultItem with similarity_score (0 for now, until semantic search is implemented)
	results := make([]models.SearchResultItem, len(records))
	for i, record := range records {
		results[i] = models.SearchResultItem{
			FeedbackRecord:  record,
			SimilarityScore: 0.0, // Will be populated when semantic search is implemented
		}
	}

	query := ""
	if req.Query != nil {
		query = *req.Query
	}

	return &models.SearchFeedbackRecordsResponse{
		Results: results,
		Query:   query,
		Count:   int64(len(results)),
	}, nil
}

// BulkDeleteFeedbackRecords deletes all feedback records matching user_identifier and optional tenant_id
func (s *FeedbackRecordsService) BulkDeleteFeedbackRecords(ctx context.Context, userIdentifier string, tenantID *string) (int64, error) {
	if userIdentifier == "" {
		return 0, fmt.Errorf("user_identifier is required")
	}

	return s.repo.BulkDelete(ctx, userIdentifier, tenantID)
}

// validateCreateRequest validates the create request
func (s *FeedbackRecordsService) validateCreateRequest(req *models.CreateFeedbackRecordRequest) error {
	if req.SourceType == "" {
		return apperrors.NewValidationError("source_type", "source_type is required")
	}

	if req.FieldID == "" {
		return apperrors.NewValidationError("field_id", "field_id is required")
	}

	if req.FieldType == "" {
		return apperrors.NewValidationError("field_type", "field_type is required")
	}

	// Validate field_type enum
	_, ok := models.ValidFieldTypes[req.FieldType]
	if !ok {
		return apperrors.NewValidationError("field_type", fmt.Sprintf("invalid field_type: %s. Must be one of: text, categorical, nps, csat, ces, rating, number, boolean, date", req.FieldType))
	}

	return nil
}

// validateUpdateRequest validates the update request
// Note: Only value fields, metadata, language, and user_identifier can be updated
func (s *FeedbackRecordsService) validateUpdateRequest(_ *models.UpdateFeedbackRecordRequest) error {
	// No validation needed for update - all fields are optional and valid
	return nil
}
