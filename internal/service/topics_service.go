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
	ExistsByTitleAndParent(ctx context.Context, title string, parentID *uuid.UUID, tenantID *string) (bool, error)
	ExistsByTitleAndParentExcluding(ctx context.Context, title string, parentID *uuid.UUID, tenantID *string, excludeID uuid.UUID) (bool, error)
	UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error
	FindSimilarTopic(ctx context.Context, embedding []float32, tenantID *string, level *int, minSimilarity float64) (*models.TopicMatch, error)
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

	// If parent_id provided, validate it exists and check cross-tenant
	if req.ParentID != nil {
		parent, err := s.repo.GetByID(ctx, *req.ParentID)
		if err != nil {
			return nil, err // Will be NotFoundError if parent doesn't exist
		}

		// Validate cross-tenant: parent.tenant_id must match request.tenant_id
		// Both can be nil (no tenant), but must match
		parentTenantID := parent.TenantID
		reqTenantID := req.TenantID

		// Check if they match (both nil, or both equal strings)
		if !tenantIDsMatch(parentTenantID, reqTenantID) {
			return nil, apperrors.NewValidationError("parent_id", "parent topic belongs to a different tenant")
		}
	}

	// Check title uniqueness within parent + tenant
	exists, err := s.repo.ExistsByTitleAndParent(ctx, req.Title, req.ParentID, req.TenantID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, apperrors.NewConflictError("topic", "topic with this title already exists under the same parent")
	}

	// Create topic (level is calculated in repository)
	topic, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	// Generate embedding asynchronously if client is configured
	// Use hierarchical path (e.g., "Performance > API") for better context
	if s.embeddingClient != nil {
		hierarchyPath := s.buildHierarchyPath(ctx, req.Title, req.ParentID)
		go s.generateEmbedding(topic.ID, hierarchyPath)
	}

	return topic, nil
}

// generateEmbedding generates and stores embedding for a topic using its hierarchical path
func (s *TopicsService) generateEmbedding(id uuid.UUID, hierarchyPath string) {
	ctx := context.Background()

	slog.Debug("generating embedding for topic", "id", id, "path", hierarchyPath)

	embedding, err := s.embeddingClient.GetEmbedding(ctx, hierarchyPath)
	if err != nil {
		slog.Error("failed to generate embedding", "record_type", "topic", "id", id, "path", hierarchyPath, "error", err)
		return
	}

	if err := s.repo.UpdateEmbedding(ctx, id, embedding); err != nil {
		slog.Error("failed to store embedding", "record_type", "topic", "id", id, "error", err)
		return
	}

	slog.Info("embedding generated successfully", "record_type", "topic", "id", id, "path", hierarchyPath)
}

// buildHierarchyPath builds the full hierarchy path for a topic (e.g., "Performance > API > Latency")
func (s *TopicsService) buildHierarchyPath(ctx context.Context, title string, parentID *uuid.UUID) string {
	if parentID == nil {
		return title
	}

	// Build path by walking up the parent chain
	var pathParts []string
	pathParts = append(pathParts, title)

	currentParentID := parentID
	for currentParentID != nil {
		parent, err := s.repo.GetByID(ctx, *currentParentID)
		if err != nil {
			// If we can't fetch parent, just use what we have
			slog.Warn("failed to fetch parent for hierarchy path", "parent_id", *currentParentID, "error", err)
			break
		}
		pathParts = append([]string{parent.Title}, pathParts...)
		currentParentID = parent.ParentID
	}

	// Join with " > " separator
	result := pathParts[0]
	for i := 1; i < len(pathParts); i++ {
		result += " > " + pathParts[i]
	}
	return result
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
		// First, fetch the existing topic to get parent_id and tenant_id
		existing, err := s.repo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}

		// Check if the new title conflicts with another topic under the same parent
		exists, err := s.repo.ExistsByTitleAndParentExcluding(ctx, *req.Title, existing.ParentID, existing.TenantID, id)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, apperrors.NewConflictError("topic", "topic with this title already exists under the same parent")
		}
	}

	topic, err := s.repo.Update(ctx, id, req)
	if err != nil {
		return nil, err
	}

	// Regenerate embedding if title was updated and client is configured
	// Use hierarchical path (e.g., "Performance > API") for better context
	if req.Title != nil && s.embeddingClient != nil {
		hierarchyPath := s.buildHierarchyPath(ctx, topic.Title, topic.ParentID)
		go s.generateEmbedding(id, hierarchyPath)
	}

	return topic, nil
}

// DeleteTopic deletes a topic by ID
func (s *TopicsService) DeleteTopic(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

// tenantIDsMatch compares two tenant IDs, handling nil values
func tenantIDsMatch(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
