// Package service implements business logic for feedback records.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
)

// ErrUserIdentifierRequired is returned when bulk delete is called without user_identifier (err113).
var ErrUserIdentifierRequired = errors.New("user_identifier is required")

// ErrEmbeddingBackfillNotConfigured is returned when BackfillEmbeddings is called without embedding inserter/queue.
var ErrEmbeddingBackfillNotConfigured = errors.New("embedding backfill not configured")

const uniqueByPeriodEmbedding = 24 * time.Hour

// FeedbackRecordsRepository defines the interface for feedback records data access.
type FeedbackRecordsRepository interface {
	Create(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	GetByID(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	List(ctx context.Context, filters *models.ListFeedbackRecordsFilters) ([]models.FeedbackRecord, error)
	Count(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (int64, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error
	ListIDsForEmbeddingBackfill(ctx context.Context) ([]uuid.UUID, error)
	Delete(ctx context.Context, id uuid.UUID) error
	BulkDelete(ctx context.Context, userIdentifier string, tenantID *string) ([]uuid.UUID, error)
}

// FeedbackRecordsService handles business logic for feedback records.
type FeedbackRecordsService struct {
	repo                 FeedbackRecordsRepository
	publisher            MessagePublisher
	embeddingInserter    FeedbackEmbeddingInserter
	embeddingQueueName   string
	embeddingMaxAttempts int
}

// NewFeedbackRecordsService creates a new feedback records service.
// embeddingInserter and embeddingQueueName are optional (for backfill); when nil/empty, BackfillEmbeddings returns an error.
func NewFeedbackRecordsService(
	repo FeedbackRecordsRepository,
	publisher MessagePublisher,
	embeddingInserter FeedbackEmbeddingInserter,
	embeddingQueueName string,
	embeddingMaxAttempts int,
) *FeedbackRecordsService {
	return &FeedbackRecordsService{
		repo:                 repo,
		publisher:            publisher,
		embeddingInserter:    embeddingInserter,
		embeddingQueueName:   embeddingQueueName,
		embeddingMaxAttempts: embeddingMaxAttempts,
	}
}

// CreateFeedbackRecord creates a new feedback record.
func (s *FeedbackRecordsService) CreateFeedbackRecord(
	ctx context.Context, req *models.CreateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	record, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create feedback record: %w", err)
	}

	s.publisher.PublishEvent(ctx, datatypes.FeedbackRecordCreated, record)

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
func (s *FeedbackRecordsService) ListFeedbackRecords(
	ctx context.Context, filters *models.ListFeedbackRecordsFilters,
) (*models.ListFeedbackRecordsResponse, error) {
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
func (s *FeedbackRecordsService) UpdateFeedbackRecord(
	ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	record, err := s.repo.Update(ctx, id, req)
	if err != nil {
		return nil, fmt.Errorf("update feedback record: %w", err)
	}

	s.publisher.PublishEventWithChangedFields(ctx, datatypes.FeedbackRecordUpdated, record, req.ChangedFields())

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
		return 0, ErrUserIdentifierRequired
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

// SetFeedbackRecordEmbedding sets the embedding for a feedback record (internal use by embeddings worker).
// It does not publish an event.
func (s *FeedbackRecordsService) SetFeedbackRecordEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error {
	if err := s.repo.UpdateEmbedding(ctx, id, embedding); err != nil {
		return fmt.Errorf("update embedding: %w", err)
	}

	return nil
}

// BackfillEmbeddings enqueues embedding jobs for all feedback records that have non-empty value_text and null embedding.
// Returns the number of jobs enqueued. Requires embeddingInserter and embeddingQueueName to be set.
func (s *FeedbackRecordsService) BackfillEmbeddings(ctx context.Context) (int, error) {
	if s.embeddingInserter == nil || s.embeddingQueueName == "" {
		return 0, ErrEmbeddingBackfillNotConfigured
	}

	ids, err := s.repo.ListIDsForEmbeddingBackfill(ctx)
	if err != nil {
		return 0, fmt.Errorf("list ids for embedding backfill: %w", err)
	}

	opts := &river.InsertOpts{
		Queue:       s.embeddingQueueName,
		MaxAttempts: s.embeddingMaxAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: uniqueByPeriodEmbedding},
	}

	enqueued := 0

	for _, id := range ids {
		_, err := s.embeddingInserter.Insert(ctx, FeedbackEmbeddingArgs{FeedbackRecordID: id}, opts)
		if err != nil {
			return enqueued, fmt.Errorf("enqueue embedding job for %s: %w", id, err)
		}

		enqueued++
	}

	return enqueued, nil
}
