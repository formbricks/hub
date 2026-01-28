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

// KnowledgeRecordsRepository defines the interface for knowledge records data access.
type KnowledgeRecordsRepository interface {
	Create(ctx context.Context, req *models.CreateKnowledgeRecordRequest) (*models.KnowledgeRecord, error)
	GetByID(ctx context.Context, id uuid.UUID) (*models.KnowledgeRecord, error)
	List(ctx context.Context, filters *models.ListKnowledgeRecordsFilters) ([]models.KnowledgeRecord, error)
	Count(ctx context.Context, filters *models.ListKnowledgeRecordsFilters) (int64, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateKnowledgeRecordRequest) (*models.KnowledgeRecord, error)
	Delete(ctx context.Context, id uuid.UUID) error
	BulkDelete(ctx context.Context, tenantID string) (int64, error)
	UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error
}

// KnowledgeRecordsService handles business logic for knowledge records
type KnowledgeRecordsService struct {
	repo            KnowledgeRecordsRepository
	embeddingClient embeddings.Client // nil if embeddings are disabled
	jobInserter     jobs.JobInserter  // nil if River is disabled (falls back to goroutines)
}

// NewKnowledgeRecordsService creates a new knowledge records service without embeddings
func NewKnowledgeRecordsService(repo KnowledgeRecordsRepository) *KnowledgeRecordsService {
	return &KnowledgeRecordsService{repo: repo}
}

// NewKnowledgeRecordsServiceWithEmbeddings creates a service with embedding support via River job queue
func NewKnowledgeRecordsServiceWithEmbeddings(repo KnowledgeRecordsRepository, embeddingClient embeddings.Client, jobInserter jobs.JobInserter) *KnowledgeRecordsService {
	return &KnowledgeRecordsService{
		repo:            repo,
		embeddingClient: embeddingClient,
		jobInserter:     jobInserter,
	}
}

// CreateKnowledgeRecord creates a new knowledge record
func (s *KnowledgeRecordsService) CreateKnowledgeRecord(ctx context.Context, req *models.CreateKnowledgeRecordRequest) (*models.KnowledgeRecord, error) {
	record, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	// Generate embedding asynchronously if client is configured
	if s.embeddingClient != nil && req.Content != "" {
		s.enqueueEmbeddingJob(ctx, record.ID, req.Content)
	}

	return record, nil
}

// enqueueEmbeddingJob enqueues an embedding job or falls back to sync generation
func (s *KnowledgeRecordsService) enqueueEmbeddingJob(ctx context.Context, id uuid.UUID, content string) {
	// If job inserter is available, use River job queue
	if s.jobInserter != nil {
		err := s.jobInserter.InsertEmbeddingJob(ctx, jobs.EmbeddingJobArgs{
			RecordID:   id,
			RecordType: jobs.RecordTypeKnowledge,
			Text:       content,
		})
		if err != nil {
			slog.Error("failed to enqueue embedding job",
				"record_type", "knowledge_record",
				"id", id,
				"error", err,
			)
			// Don't fail the request - embedding can be backfilled later
		}
		return
	}

	// Fallback to sync generation in a goroutine (legacy behavior for tests or when River is disabled)
	if s.embeddingClient != nil {
		go s.generateEmbeddingSync(id, content)
	}
}

// generateEmbeddingSync generates and stores embedding synchronously (used as fallback)
func (s *KnowledgeRecordsService) generateEmbeddingSync(id uuid.UUID, content string) {
	ctx := context.Background()

	slog.Debug("generating embedding for knowledge record (sync)", "id", id, "content_length", len(content))

	embedding, err := s.embeddingClient.GetEmbedding(ctx, content)
	if err != nil {
		slog.Error("failed to generate embedding", "record_type", "knowledge_record", "id", id, "error", err)
		return
	}

	if err := s.repo.UpdateEmbedding(ctx, id, embedding); err != nil {
		slog.Error("failed to store embedding", "record_type", "knowledge_record", "id", id, "error", err)
		return
	}

	slog.Info("embedding generated successfully", "record_type", "knowledge_record", "id", id)
}

// GetKnowledgeRecord retrieves a single knowledge record by ID
func (s *KnowledgeRecordsService) GetKnowledgeRecord(ctx context.Context, id uuid.UUID) (*models.KnowledgeRecord, error) {
	return s.repo.GetByID(ctx, id)
}

// ListKnowledgeRecords retrieves a list of knowledge records with optional filters
func (s *KnowledgeRecordsService) ListKnowledgeRecords(ctx context.Context, filters *models.ListKnowledgeRecordsFilters) (*models.ListKnowledgeRecordsResponse, error) {
	// Set default limit if not provided
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

	return &models.ListKnowledgeRecordsResponse{
		Data:   records,
		Total:  total,
		Limit:  filters.Limit,
		Offset: filters.Offset,
	}, nil
}

// UpdateKnowledgeRecord updates an existing knowledge record
func (s *KnowledgeRecordsService) UpdateKnowledgeRecord(ctx context.Context, id uuid.UUID, req *models.UpdateKnowledgeRecordRequest) (*models.KnowledgeRecord, error) {
	record, err := s.repo.Update(ctx, id, req)
	if err != nil {
		return nil, err
	}

	// Regenerate embedding if content was updated and client is configured
	if req.Content != nil && *req.Content != "" && s.embeddingClient != nil {
		s.enqueueEmbeddingJob(ctx, id, *req.Content)
	}

	return record, nil
}

// DeleteKnowledgeRecord deletes a knowledge record by ID
func (s *KnowledgeRecordsService) DeleteKnowledgeRecord(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

// BulkDeleteKnowledgeRecords deletes all knowledge records matching tenant_id
func (s *KnowledgeRecordsService) BulkDeleteKnowledgeRecords(ctx context.Context, tenantID string) (int64, error) {
	if tenantID == "" {
		return 0, fmt.Errorf("tenant_id is required")
	}

	return s.repo.BulkDelete(ctx, tenantID)
}
