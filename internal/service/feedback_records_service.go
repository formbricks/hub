package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/formbricks/hub/internal/embeddings"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
)

// DefaultClassificationThreshold is the minimum similarity score for topic classification
const DefaultClassificationThreshold = 0.5

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
}

// TopicClassifier defines the interface for topic classification via vector similarity
type TopicClassifier interface {
	FindSimilarTopic(ctx context.Context, embedding []float32, tenantID *string, minSimilarity float64) (*models.TopicMatch, error)
}

// FeedbackRecordsService handles business logic for feedback records
type FeedbackRecordsService struct {
	repo            FeedbackRecordsRepository
	embeddingClient embeddings.Client  // nil if embeddings are disabled
	topicClassifier TopicClassifier    // nil if classification is disabled
}

// NewFeedbackRecordsService creates a new feedback records service
func NewFeedbackRecordsService(repo FeedbackRecordsRepository) *FeedbackRecordsService {
	return &FeedbackRecordsService{repo: repo}
}

// NewFeedbackRecordsServiceWithEmbeddings creates a service with embedding support
func NewFeedbackRecordsServiceWithEmbeddings(repo FeedbackRecordsRepository, embeddingClient embeddings.Client) *FeedbackRecordsService {
	return &FeedbackRecordsService{
		repo:            repo,
		embeddingClient: embeddingClient,
	}
}

// NewFeedbackRecordsServiceWithClassification creates a service with embedding and classification support
func NewFeedbackRecordsServiceWithClassification(repo FeedbackRecordsRepository, embeddingClient embeddings.Client, topicClassifier TopicClassifier) *FeedbackRecordsService {
	return &FeedbackRecordsService{
		repo:            repo,
		embeddingClient: embeddingClient,
		topicClassifier: topicClassifier,
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
		go s.enrichRecord(record.ID, *req.ValueText, req.TenantID)
	}

	return record, nil
}

// enrichRecord generates embedding, classifies against topics, and enriches the feedback record
func (s *FeedbackRecordsService) enrichRecord(id uuid.UUID, text string, tenantID *string) {
	ctx := context.Background()

	// 1. Generate embedding
	embedding, err := s.embeddingClient.GetEmbedding(ctx, text)
	if err != nil {
		slog.Error("failed to generate embedding", "record_type", "feedback_record", "id", id, "error", err)
		return
	}

	enrichReq := &models.UpdateFeedbackEnrichmentRequest{
		Embedding: embedding,
	}

	// 2. Classify against topics if classifier is available
	if s.topicClassifier != nil {
		match, err := s.topicClassifier.FindSimilarTopic(ctx, embedding, tenantID, DefaultClassificationThreshold)
		if err != nil {
			slog.Error("failed to classify feedback", "record_type", "feedback_record", "id", id, "error", err)
		} else if match != nil {
			enrichReq.TopicID = &match.TopicID
			enrichReq.ClassificationConfidence = &match.Similarity
			slog.Debug("classified feedback", "id", id, "topic_id", match.TopicID, "topic_title", match.Title, "confidence", match.Similarity)
		}
	}

	// 3. Store enrichment data
	if err := s.repo.UpdateEnrichment(ctx, id, enrichReq); err != nil {
		slog.Error("failed to store enrichment", "record_type", "feedback_record", "id", id, "error", err)
	}
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
	record, err := s.repo.Update(ctx, id, req)
	if err != nil {
		return nil, err
	}

	// Regenerate embedding if text was updated and client is configured
	if s.embeddingClient != nil && req.ValueText != nil && *req.ValueText != "" {
		// Check if this is a text field (the record has the field_type)
		if record.FieldType == "text" {
			go s.enrichRecord(id, *req.ValueText, record.TenantID)
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
