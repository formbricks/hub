package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/formbricks/hub/internal/embeddings"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
)

// Similarity thresholds based on topic level
const (
	// ThemeThreshold is the minimum similarity for theme (level 1) topics
	// Themes are broader, so we accept lower similarity
	ThemeThreshold = 0.35

	// SubtopicThreshold is the minimum similarity for subtopic (level 2+) topics
	// Subtopics are specific, so we require higher confidence
	SubtopicThreshold = 0.50
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
	UpdateEnrichment(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackEnrichmentRequest) error
	ListBySimilarity(ctx context.Context, topicEmbedding []float32, minSimilarity float64, filters *models.ListFeedbackRecordsFilters) ([]models.FeedbackRecord, error)
	CountBySimilarity(ctx context.Context, topicEmbedding []float32, minSimilarity float64, filters *models.ListFeedbackRecordsFilters) (int64, error)
}

// TopicLookup defines the interface for looking up topic information (for vector search)
type TopicLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (*models.Topic, error)
	GetEmbedding(ctx context.Context, id uuid.UUID) ([]float32, error)
}

// FeedbackRecordsService handles business logic for feedback records
type FeedbackRecordsService struct {
	repo            FeedbackRecordsRepository
	embeddingClient embeddings.Client // nil if embeddings are disabled
	topicLookup     TopicLookup       // nil if topic-based filtering is disabled
}

// NewFeedbackRecordsService creates a new feedback records service
func NewFeedbackRecordsService(repo FeedbackRecordsRepository) *FeedbackRecordsService {
	return &FeedbackRecordsService{repo: repo}
}

// NewFeedbackRecordsServiceWithEmbeddings creates a service with embedding support
func NewFeedbackRecordsServiceWithEmbeddings(repo FeedbackRecordsRepository, embeddingClient embeddings.Client, topicLookup TopicLookup) *FeedbackRecordsService {
	return &FeedbackRecordsService{
		repo:            repo,
		embeddingClient: embeddingClient,
		topicLookup:     topicLookup,
	}
}

// CreateFeedbackRecord creates a new feedback record
func (s *FeedbackRecordsService) CreateFeedbackRecord(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	record, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	// Generate embedding for text feedback asynchronously if client is configured
	if s.embeddingClient != nil && req.FieldType == "text" && req.ValueText != nil && *req.ValueText != "" {
		go s.generateEmbedding(record.ID, *req.ValueText)
	}

	return record, nil
}

// generateEmbedding generates and stores embedding for a feedback record
func (s *FeedbackRecordsService) generateEmbedding(id uuid.UUID, text string) {
	ctx := context.Background()

	slog.Debug("generating embedding for feedback", "id", id, "text_length", len(text))

	embedding, err := s.embeddingClient.GetEmbedding(ctx, text)
	if err != nil {
		slog.Error("failed to generate embedding", "record_type", "feedback_record", "id", id, "error", err)
		return
	}

	enrichReq := &models.UpdateFeedbackEnrichmentRequest{
		Embedding: embedding,
	}

	if err := s.repo.UpdateEnrichment(ctx, id, enrichReq); err != nil {
		slog.Error("failed to store embedding", "record_type", "feedback_record", "id", id, "error", err)
		return
	}

	slog.Info("embedding generated successfully", "record_type", "feedback_record", "id", id)
}

// GetFeedbackRecord retrieves a single feedback record by ID
func (s *FeedbackRecordsService) GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error) {
	return s.repo.GetByID(ctx, id)
}

// ListFeedbackRecords retrieves a list of feedback records with optional filters
// If TopicID filter is provided and topic lookup is configured, uses vector similarity search
func (s *FeedbackRecordsService) ListFeedbackRecords(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (*models.ListFeedbackRecordsResponse, error) {
	// Set default limit if not provided
	if filters.Limit <= 0 {
		filters.Limit = 100
	}

	// If topic_id filter is provided, use vector similarity search
	if filters.TopicID != nil {
		return s.listByTopicSimilarity(ctx, *filters.TopicID, filters)
	}

	// Standard listing without vector search
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

// listByTopicSimilarity retrieves feedback records similar to a topic's embedding
func (s *FeedbackRecordsService) listByTopicSimilarity(ctx context.Context, topicID uuid.UUID, filters *models.ListFeedbackRecordsFilters) (*models.ListFeedbackRecordsResponse, error) {
	if s.topicLookup == nil {
		return nil, fmt.Errorf("topic-based filtering is not enabled")
	}

	// Get the topic to determine its level and embedding
	topic, err := s.topicLookup.GetByID(ctx, topicID)
	if err != nil {
		return nil, fmt.Errorf("failed to get topic: %w", err)
	}

	// Get the topic's embedding
	topicEmbedding, err := s.topicLookup.GetEmbedding(ctx, topicID)
	if err != nil {
		return nil, fmt.Errorf("failed to get topic embedding: %w", err)
	}

	if topicEmbedding == nil {
		slog.Warn("topic has no embedding, cannot perform similarity search", "topic_id", topicID)
		// Return empty results
		return &models.ListFeedbackRecordsResponse{
			Data:   []models.FeedbackRecord{},
			Total:  0,
			Limit:  filters.Limit,
			Offset: filters.Offset,
		}, nil
	}

	// Determine threshold: use custom if provided, otherwise based on topic level
	var minSimilarity float64
	if filters.MinSimilarity != nil {
		minSimilarity = *filters.MinSimilarity
	} else if topic.Level == 1 {
		minSimilarity = ThemeThreshold
	} else {
		minSimilarity = SubtopicThreshold
	}

	slog.Debug("performing similarity search",
		"topic_id", topicID,
		"topic_title", topic.Title,
		"topic_level", topic.Level,
		"min_similarity", minSimilarity,
		"custom_threshold", filters.MinSimilarity != nil,
	)

	// Perform similarity search
	records, err := s.repo.ListBySimilarity(ctx, topicEmbedding, minSimilarity, filters)
	if err != nil {
		return nil, err
	}

	total, err := s.repo.CountBySimilarity(ctx, topicEmbedding, minSimilarity, filters)
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
	record, err := s.repo.Update(ctx, id, req)
	if err != nil {
		return nil, err
	}

	// Regenerate embedding if text was updated and client is configured
	if s.embeddingClient != nil && req.ValueText != nil && *req.ValueText != "" {
		if record.FieldType == "text" {
			go s.generateEmbedding(id, *req.ValueText)
		}
	}

	return record, nil
}

// DeleteFeedbackRecord deletes a feedback record by ID
func (s *FeedbackRecordsService) DeleteFeedbackRecord(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

// BulkDeleteFeedbackRecords deletes all feedback records matching user_identifier and optional tenant_id
func (s *FeedbackRecordsService) BulkDeleteFeedbackRecords(ctx context.Context, userIdentifier string, tenantID *string) (int64, error) {
	if userIdentifier == "" {
		return 0, fmt.Errorf("user_identifier is required")
	}

	return s.repo.BulkDelete(ctx, userIdentifier, tenantID)
}
