package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/xernobyl/formbricks_worktrial/internal/models"
	"github.com/xernobyl/formbricks_worktrial/internal/repository"
)

// ExperienceService handles business logic for experience data
type ExperienceService struct {
	repo *repository.ExperienceRepository
}

// NewExperienceService creates a new experience service
func NewExperienceService(repo *repository.ExperienceRepository) *ExperienceService {
	return &ExperienceService{repo: repo}
}

// CreateExperience creates a new experience data record
func (s *ExperienceService) CreateExperience(ctx context.Context, req *models.CreateExperienceRequest) (*models.ExperienceData, error) {
	if err := s.validateCreateRequest(req); err != nil {
		return nil, err
	}

	return s.repo.Create(ctx, req)
}

// GetExperience retrieves a single experience by ID
func (s *ExperienceService) GetExperience(ctx context.Context, id uuid.UUID) (*models.ExperienceData, error) {
	return s.repo.GetByID(ctx, id)
}

// ListExperiences retrieves a list of experiences with optional filters
func (s *ExperienceService) ListExperiences(ctx context.Context, filters *models.ListExperiencesFilters) ([]models.ExperienceData, error) {
	if filters.Limit <= 0 {
		filters.Limit = 100 // Default limit
	}
	if filters.Limit > 1000 {
		filters.Limit = 1000 // Max limit
	}

	return s.repo.List(ctx, filters)
}

// UpdateExperience updates an existing experience
func (s *ExperienceService) UpdateExperience(ctx context.Context, id uuid.UUID, req *models.UpdateExperienceRequest) (*models.ExperienceData, error) {
	if err := s.validateUpdateRequest(req); err != nil {
		return nil, err
	}

	return s.repo.Update(ctx, id, req)
}

// DeleteExperience deletes an experience by ID
func (s *ExperienceService) DeleteExperience(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

// SearchExperiences performs advanced search with pagination
func (s *ExperienceService) SearchExperiences(ctx context.Context, req *models.SearchExperiencesRequest) (*models.SearchExperiencesResponse, error) {
	// Set default page size and enforce limits
	if req.PageSize <= 0 {
		req.PageSize = 20 // Default page size
	}
	if req.PageSize > 40 {
		req.PageSize = 40 // Max page size
	}

	// Ensure page is not negative
	if req.Page < 0 {
		req.Page = 0
	}

	// Call repository search
	experiences, totalCount, err := s.repo.Search(ctx, req)
	if err != nil {
		return nil, err
	}

	// Calculate total pages
	totalPages := totalCount / req.PageSize
	if totalCount%req.PageSize > 0 {
		totalPages++
	}

	// Ensure we have at least 0 data
	if experiences == nil {
		experiences = []models.ExperienceData{}
	}

	return &models.SearchExperiencesResponse{
		Data:       experiences,
		Page:       req.Page,
		PageSize:   req.PageSize,
		TotalCount: totalCount,
		TotalPages: totalPages,
	}, nil
}

// validateCreateRequest validates the create request
func (s *ExperienceService) validateCreateRequest(req *models.CreateExperienceRequest) error {
	if req.SourceType == "" {
		return fmt.Errorf("source_type is required")
	}

	if req.FieldID == "" {
		return fmt.Errorf("field_id is required")
	}

	if req.FieldType == "" {
		return fmt.Errorf("field_type is required")
	}

	return nil
}

// validateUpdateRequest validates the update request
func (s *ExperienceService) validateUpdateRequest(req *models.UpdateExperienceRequest) error {
	if req.SourceType != nil && *req.SourceType == "" {
		return fmt.Errorf("source_type cannot be empty")
	}

	if req.FieldID != nil && *req.FieldID == "" {
		return fmt.Errorf("field_id cannot be empty")
	}

	if req.FieldType != nil && *req.FieldType == "" {
		return fmt.Errorf("field_type cannot be empty")
	}

	return nil
}
