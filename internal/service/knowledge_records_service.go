package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/formbricks/hub/internal/embeddings"
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
}

// NewKnowledgeRecordsService creates a new knowledge records service
func NewKnowledgeRecordsService(repo KnowledgeRecordsRepository) *KnowledgeRecordsService {
	return &KnowledgeRecordsService{repo: repo}
}

// NewKnowledgeRecordsServiceWithEmbeddings creates a service with embedding support
func NewKnowledgeRecordsServiceWithEmbeddings(repo KnowledgeRecordsRepository, embeddingClient embeddings.Client) *KnowledgeRecordsService {
	return &KnowledgeRecordsService{
		repo:            repo,
		embeddingClient: embeddingClient,
	}
}

// CreateKnowledgeRecord creates a new knowledge record
func (s *KnowledgeRecordsService) CreateKnowledgeRecord(ctx context.Context, req *models.CreateKnowledgeRecordRequest) (*models.KnowledgeRecord, error) {
	record, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	// Generate embedding asynchronously if client is configured
	if s.embeddingClient != nil {
		go s.generateEmbedding(record.ID, req.Content)
	}

	return record, nil
}

// generateEmbedding generates and stores embedding for a knowledge record
func (s *KnowledgeRecordsService) generateEmbedding(id uuid.UUID, content string) {
	ctx := context.Background()

	embedding, err := s.embeddingClient.GetEmbedding(ctx, content)
	if err != nil {
		slog.Error("failed to generate embedding", "record_type", "knowledge_record", "id", id, "error", err)
		return
	}

	if err := s.repo.UpdateEmbedding(ctx, id, embedding); err != nil {
		slog.Error("failed to store embedding", "record_type", "knowledge_record", "id", id, "error", err)
	}
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
	if req.Content != nil && s.embeddingClient != nil {
		go s.generateEmbedding(id, *req.Content)
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
