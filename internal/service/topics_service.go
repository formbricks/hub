package service

import (
	"context"
	"log/slog"

	"github.com/formbricks/hub/internal/embeddings"
	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
)

// TopicsRepository defines the interface for topics data access.
type TopicsRepository interface {
	Create(ctx context.Context, req *models.CreateTopicRequest) (*models.Topic, error)
	GetByID(ctx context.Context, id uuid.UUID) (*models.Topic, error)
	List(ctx context.Context, filters *models.ListTopicsFilters) ([]models.Topic, error)
	Count(ctx context.Context, filters *models.ListTopicsFilters) (int64, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateTopicRequest) (*models.Topic, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ExistsByTitleAndLevel(ctx context.Context, title string, level int, tenantID *string) (bool, error)
	ExistsByTitleAndLevelExcluding(ctx context.Context, title string, level int, tenantID *string, excludeID uuid.UUID) (bool, error)
	UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error
	FindSimilarTopic(ctx context.Context, embedding []float32, tenantID *string, level *int, minSimilarity float64) (*models.TopicMatch, error)
	GetChildTopics(ctx context.Context, parentID uuid.UUID, tenantID *string, limit int) ([]models.Topic, error)
}

// TopicsService handles business logic for topics
type TopicsService struct {
	repo            TopicsRepository
	embeddingClient embeddings.Client // nil if embeddings are disabled
}

// NewTopicsService creates a new topics service
func NewTopicsService(repo TopicsRepository) *TopicsService {
	return &TopicsService{repo: repo}
}

// NewTopicsServiceWithEmbeddings creates a service with embedding support
func NewTopicsServiceWithEmbeddings(repo TopicsRepository, embeddingClient embeddings.Client) *TopicsService {
	return &TopicsService{
		repo:            repo,
		embeddingClient: embeddingClient,
	}
}

// CreateTopic creates a new topic with validation
func (s *TopicsService) CreateTopic(ctx context.Context, req *models.CreateTopicRequest) (*models.Topic, error) {
	// Normalize empty string tenant_id to nil
	if req.TenantID != nil && *req.TenantID == "" {
		req.TenantID = nil
	}

	// Validate level
	if req.Level < 1 || req.Level > 2 {
		return nil, apperrors.NewValidationError("level", "level must be 1 or 2")
	}

	// Validate parent_id based on level
	if req.Level == 1 && req.ParentID != nil {
		return nil, apperrors.NewValidationError("parent_id", "Level 1 topics cannot have a parent")
	}
	if req.Level == 2 && req.ParentID == nil {
		return nil, apperrors.NewValidationError("parent_id", "Level 2 topics must have a parent_id")
	}

	// If Level 2, validate that parent exists and is Level 1
	if req.ParentID != nil {
		parent, err := s.repo.GetByID(ctx, *req.ParentID)
		if err != nil {
			return nil, apperrors.NewValidationError("parent_id", "parent topic not found")
		}
		if parent.Level != 1 {
			return nil, apperrors.NewValidationError("parent_id", "parent must be a Level 1 topic")
		}
	}

	// Check title uniqueness within level + tenant
	exists, err := s.repo.ExistsByTitleAndLevel(ctx, req.Title, req.Level, req.TenantID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, apperrors.NewConflictError("topic", "topic with this title already exists at this level")
	}

	// Create topic
	topic, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	// Generate embedding asynchronously if client is configured
	if s.embeddingClient != nil {
		go s.generateEmbedding(topic.ID, req.Title)
	}

	return topic, nil
}

// generateEmbedding generates and stores embedding for a topic
func (s *TopicsService) generateEmbedding(id uuid.UUID, title string) {
	ctx := context.Background()

	embedding, err := s.embeddingClient.GetEmbedding(ctx, title)
	if err != nil {
		slog.Error("failed to generate embedding", "record_type", "topic", "id", id, "error", err)
		return
	}

	if err := s.repo.UpdateEmbedding(ctx, id, embedding); err != nil {
		slog.Error("failed to store embedding", "record_type", "topic", "id", id, "error", err)
	}
}

// GetTopic retrieves a single topic by ID
func (s *TopicsService) GetTopic(ctx context.Context, id uuid.UUID) (*models.Topic, error) {
	return s.repo.GetByID(ctx, id)
}

// ListTopics retrieves a list of topics with optional filters
func (s *TopicsService) ListTopics(ctx context.Context, filters *models.ListTopicsFilters) (*models.ListTopicsResponse, error) {
	// Set default limit if not provided
	if filters.Limit <= 0 {
		filters.Limit = 100 // Default limit
	}

	topics, err := s.repo.List(ctx, filters)
	if err != nil {
		return nil, err
	}

	total, err := s.repo.Count(ctx, filters)
	if err != nil {
		return nil, err
	}

	return &models.ListTopicsResponse{
		Data:   topics,
		Total:  total,
		Limit:  filters.Limit,
		Offset: filters.Offset,
	}, nil
}

// UpdateTopic updates an existing topic
func (s *TopicsService) UpdateTopic(ctx context.Context, id uuid.UUID, req *models.UpdateTopicRequest) (*models.Topic, error) {
	// If title is being updated, check uniqueness (excluding current topic)
	if req.Title != nil {
		// First, fetch the existing topic to get level and tenant_id
		existing, err := s.repo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}

		// Check if the new title conflicts with another topic at the same level
		exists, err := s.repo.ExistsByTitleAndLevelExcluding(ctx, *req.Title, existing.Level, existing.TenantID, id)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, apperrors.NewConflictError("topic", "topic with this title already exists at this level")
		}
	}

	topic, err := s.repo.Update(ctx, id, req)
	if err != nil {
		return nil, err
	}

	// Regenerate embedding if title was updated and client is configured
	if req.Title != nil && s.embeddingClient != nil {
		go s.generateEmbedding(id, *req.Title)
	}

	return topic, nil
}

// DeleteTopic deletes a topic by ID
func (s *TopicsService) DeleteTopic(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

// GetChildTopics retrieves Level 2 topics that are children of a Level 1 topic
func (s *TopicsService) GetChildTopics(ctx context.Context, parentID uuid.UUID, tenantID *string, limit int) ([]models.Topic, error) {
	// Validate that the parent topic exists and is Level 1
	topic, err := s.repo.GetByID(ctx, parentID)
	if err != nil {
		return nil, err
	}

	if topic.Level != 1 {
		return nil, apperrors.NewValidationError("id", "parent must be a Level 1 topic")
	}

	return s.repo.GetChildTopics(ctx, parentID, tenantID, limit)
}
