package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/formbricks/hub/internal/embeddings"
	"github.com/formbricks/hub/internal/jobs"
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
	UpdateEnrichment(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackEnrichmentRequest) error
	// ListByTopicWithDescendants finds feedback assigned to a topic or its descendants (direct lookup)
	ListByTopicWithDescendants(ctx context.Context, topicID uuid.UUID, filters *models.ListFeedbackRecordsFilters) ([]models.FeedbackRecord, int64, error)
	// ListBySimilarityWithDescendants finds feedback similar to a topic AND all its descendants (vector search)
	ListBySimilarityWithDescendants(ctx context.Context, topicID uuid.UUID, levelThresholds map[int]float64, defaultThreshold float64, filters *models.ListFeedbackRecordsFilters) ([]models.FeedbackRecord, int64, error)
}

// FeedbackRecordsService handles business logic for feedback records
type FeedbackRecordsService struct {
	repo            FeedbackRecordsRepository
	embeddingClient embeddings.Client // nil if embeddings are disabled
	jobInserter     jobs.JobInserter  // nil if River is disabled (falls back to goroutines)
}

// NewFeedbackRecordsService creates a new feedback records service without embeddings
func NewFeedbackRecordsService(repo FeedbackRecordsRepository) *FeedbackRecordsService {
	return &FeedbackRecordsService{repo: repo}
}

// NewFeedbackRecordsServiceWithEmbeddings creates a service with embedding support via River job queue
func NewFeedbackRecordsServiceWithEmbeddings(repo FeedbackRecordsRepository, embeddingClient embeddings.Client, jobInserter jobs.JobInserter) *FeedbackRecordsService {
	return &FeedbackRecordsService{
		repo:            repo,
		embeddingClient: embeddingClient,
		jobInserter:     jobInserter,
	}
}

// CreateFeedbackRecord creates a new feedback record
func (s *FeedbackRecordsService) CreateFeedbackRecord(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	record, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	// Generate embedding for text feedback asynchronously
	if s.shouldGenerateEmbedding(req.FieldType, req.ValueText) {
		s.enqueueEmbeddingJob(ctx, record.ID, *req.ValueText)
	}

	return record, nil
}

// shouldGenerateEmbedding checks if embedding should be generated for the given field
func (s *FeedbackRecordsService) shouldGenerateEmbedding(fieldType string, valueText *string) bool {
	if s.embeddingClient == nil {
		return false
	}
	if fieldType != "text" {
		return false
	}
	if valueText == nil || *valueText == "" {
		return false
	}
	return true
}

// enqueueEmbeddingJob enqueues an embedding job or falls back to sync generation
func (s *FeedbackRecordsService) enqueueEmbeddingJob(ctx context.Context, id uuid.UUID, text string) {
	// If job inserter is available, use River job queue
	if s.jobInserter != nil {
		err := s.jobInserter.InsertEmbeddingJob(ctx, jobs.EmbeddingJobArgs{
			RecordID:   id,
			RecordType: jobs.RecordTypeFeedback,
			Text:       text,
		})
		if err != nil {
			slog.Error("failed to enqueue embedding job",
				"record_type", "feedback_record",
				"id", id,
				"error", err,
			)
			// Don't fail the request - embedding can be backfilled later
		}
		return
	}

	// Fallback to sync generation in a goroutine (legacy behavior for tests or when River is disabled)
	if s.embeddingClient != nil {
		go s.generateEmbeddingSync(id, text)
	}
}

// generateEmbeddingSync generates and stores embedding synchronously (used as fallback)
func (s *FeedbackRecordsService) generateEmbeddingSync(id uuid.UUID, text string) {
	ctx := context.Background()

	slog.Debug("generating embedding for feedback (sync)", "id", id, "text_length", len(text))

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
// If TopicID filter is provided, uses vector similarity search with hierarchical aggregation
func (s *FeedbackRecordsService) ListFeedbackRecords(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (*models.ListFeedbackRecordsResponse, error) {
	// Set default limit if not provided
	if filters.Limit <= 0 {
		filters.Limit = 100
	}

	// If topic_id filter is provided, use direct lookup (or similarity if explicitly requested)
	if filters.TopicID != nil {
		return s.listByTopic(ctx, *filters.TopicID, filters)
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

// listByTopic retrieves feedback records assigned to a topic or its descendants.
// Uses direct topic_id lookup (fast, pre-computed during taxonomy generation).
// Falls back to similarity search if UseSimilarity filter is set.
func (s *FeedbackRecordsService) listByTopic(ctx context.Context, topicID uuid.UUID, filters *models.ListFeedbackRecordsFilters) (*models.ListFeedbackRecordsResponse, error) {
	// Check if similarity search is explicitly requested
	if filters.UseSimilarity {
		return s.listByTopicSimilarity(ctx, topicID, filters)
	}

	// Default: Use direct topic_id lookup (faster, uses pre-computed assignments)
	slog.Debug("using direct topic_id lookup",
		"topic_id", topicID,
	)

	records, total, err := s.repo.ListByTopicWithDescendants(ctx, topicID, filters)
	if err != nil {
		return nil, fmt.Errorf("failed to list feedback by topic: %w", err)
	}

	return &models.ListFeedbackRecordsResponse{
		Data:   records,
		Total:  total,
		Limit:  filters.Limit,
		Offset: filters.Offset,
	}, nil
}

// listByTopicSimilarity retrieves feedback records similar to a topic AND all its descendants.
// Uses optimized single-query approach with level-based thresholds.
// This is slower but can find matches for unclassified feedback.
func (s *FeedbackRecordsService) listByTopicSimilarity(ctx context.Context, topicID uuid.UUID, filters *models.ListFeedbackRecordsFilters) (*models.ListFeedbackRecordsResponse, error) {
	// Determine thresholds to use
	var levelThresholds map[int]float64
	if filters.MinSimilarity != nil {
		// Custom threshold overrides level-based thresholds
		// Apply same threshold to all levels
		threshold := *filters.MinSimilarity
		levelThresholds = map[int]float64{
			1: threshold,
			2: threshold,
			3: threshold,
			4: threshold,
			5: threshold,
		}
		slog.Debug("using custom similarity threshold for all levels",
			"topic_id", topicID,
			"threshold", threshold,
		)
	} else {
		// Use level-based thresholds from models
		levelThresholds = models.LevelThresholds
		slog.Debug("using level-based similarity thresholds",
			"topic_id", topicID,
			"thresholds", levelThresholds,
		)
	}

	// Perform optimized similarity search with descendants
	records, total, err := s.repo.ListBySimilarityWithDescendants(
		ctx,
		topicID,
		levelThresholds,
		models.DefaultThreshold,
		filters,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to search feedback by topic similarity: %w", err)
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

	// Regenerate embedding if text was updated
	if record.FieldType == "text" && s.shouldGenerateEmbedding(record.FieldType, req.ValueText) {
		s.enqueueEmbeddingJob(ctx, id, *req.ValueText)
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
