// Package service implements business logic for feedback records.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/cursor"
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
	ListAfterCursor(
		ctx context.Context, filters *models.ListFeedbackRecordsFilters,
		cursorCollectedAt time.Time, cursorID uuid.UUID,
	) ([]models.FeedbackRecord, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	Delete(ctx context.Context, id uuid.UUID) error
	BulkDelete(ctx context.Context, userIdentifier string, tenantID *string) ([]uuid.UUID, error)
}

// EmbeddingsRepository defines the interface for embeddings table access.
type EmbeddingsRepository interface {
	Upsert(ctx context.Context, feedbackRecordID uuid.UUID, model string, embedding []float32) error
	DeleteByFeedbackRecordAndModel(ctx context.Context, feedbackRecordID uuid.UUID, model string) error
	ListFeedbackRecordIDsForBackfill(ctx context.Context, model string) ([]uuid.UUID, error)
}

// FeedbackRecordsService handles business logic for feedback records.
type FeedbackRecordsService struct {
	repo                 FeedbackRecordsRepository
	embeddingsRepo       EmbeddingsRepository
	embeddingModel       string
	publisher            MessagePublisher
	embeddingInserter    FeedbackEmbeddingInserter
	embeddingQueueName   string
	embeddingMaxAttempts int
}

// NewFeedbackRecordsService creates a new feedback records service.
// publisher may be nil when the service is used only for backfill (BackfillEmbeddings does not use the publisher).
// embeddingInserter and embeddingQueueName are optional (for backfill); when nil/empty, BackfillEmbeddings returns an error.
// Call SetEmbeddingInserter after the River client is created to enable backfill without building the service twice.
// embeddingsRepo and embeddingModel are required for SetEmbedding and BackfillEmbeddings (from EMBEDDING_PROVIDER and EMBEDDING_MODEL).
func NewFeedbackRecordsService(
	repo FeedbackRecordsRepository,
	embeddingsRepo EmbeddingsRepository,
	embeddingModel string,
	publisher MessagePublisher,
	embeddingInserter FeedbackEmbeddingInserter,
	embeddingQueueName string,
	embeddingMaxAttempts int,
) *FeedbackRecordsService {
	return &FeedbackRecordsService{
		repo:                 repo,
		embeddingsRepo:       embeddingsRepo,
		embeddingModel:       embeddingModel,
		publisher:            publisher,
		embeddingInserter:    embeddingInserter,
		embeddingQueueName:   embeddingQueueName,
		embeddingMaxAttempts: embeddingMaxAttempts,
	}
}

// SetEmbeddingInserter sets the River inserter for embedding jobs (e.g. after River client is created).
// This allows a single service instance to be used by both handlers and the embedding worker.
func (s *FeedbackRecordsService) SetEmbeddingInserter(inserter FeedbackEmbeddingInserter) {
	s.embeddingInserter = inserter
}

// CreateFeedbackRecord creates a new feedback record.
func (s *FeedbackRecordsService) CreateFeedbackRecord(
	ctx context.Context, req *models.CreateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	record, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create feedback record: %w", err)
	}

	if s.publisher != nil {
		s.publisher.PublishEvent(ctx, datatypes.FeedbackRecordCreated, record)
	}

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
// Uses cursor-based pagination: omit cursor for first page, use next_cursor for subsequent pages.
func (s *FeedbackRecordsService) ListFeedbackRecords(
	ctx context.Context, filters *models.ListFeedbackRecordsFilters,
) (*models.ListFeedbackRecordsResponse, error) {
	if filters.Limit <= 0 {
		filters.Limit = 100
	}

	cursorStr := strings.TrimSpace(filters.Cursor)

	var (
		records []models.FeedbackRecord
		err     error
	)

	if cursorStr != "" {
		collectedAt, id, decErr := cursor.Decode(cursorStr)
		if decErr != nil {
			return nil, fmt.Errorf("decode cursor: %w", decErr)
		}

		records, err = s.repo.ListAfterCursor(ctx, filters, collectedAt, id)
	} else {
		records, err = s.repo.List(ctx, filters)
	}

	if err != nil {
		return nil, fmt.Errorf("list feedback records: %w", err)
	}

	meta, err := BuildListPaginationMeta(filters.Limit, len(records), func() (string, error) {
		last := records[len(records)-1]

		return cursor.Encode(last.CollectedAt, last.ID)
	})
	if err != nil {
		return nil, fmt.Errorf("encode next cursor: %w", err)
	}

	return &models.ListFeedbackRecordsResponse{
		Data:       records,
		Limit:      meta.Limit,
		NextCursor: meta.NextCursor,
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

	if s.publisher != nil {
		s.publisher.PublishEventWithChangedFields(ctx, datatypes.FeedbackRecordUpdated, record, req.ChangedFields())
	}

	return record, nil
}

// DeleteFeedbackRecord deletes a feedback record by ID.
// Publishes FeedbackRecordDeleted with data = [id] (array of deleted IDs) for consistency with bulk delete.
func (s *FeedbackRecordsService) DeleteFeedbackRecord(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete feedback record: %w", err)
	}

	if s.publisher != nil {
		s.publisher.PublishEvent(ctx, datatypes.FeedbackRecordDeleted, []uuid.UUID{id})
	}

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

	if len(ids) > 0 && s.publisher != nil {
		s.publisher.PublishEvent(ctx, datatypes.FeedbackRecordDeleted, ids)
	}

	return len(ids), nil
}

// SetEmbedding sets or clears the embedding for a feedback record and model (internal use by embeddings worker).
// If embedding is nil, the row for (feedbackRecordID, model) is deleted; otherwise upserted.
// It does not publish an event.
func (s *FeedbackRecordsService) SetEmbedding(
	ctx context.Context, feedbackRecordID uuid.UUID, model string, embedding []float32,
) error {
	if embedding == nil {
		if err := s.embeddingsRepo.DeleteByFeedbackRecordAndModel(ctx, feedbackRecordID, model); err != nil {
			return fmt.Errorf("delete embedding: %w", err)
		}

		return nil
	}

	if err := s.embeddingsRepo.Upsert(ctx, feedbackRecordID, model, embedding); err != nil {
		return fmt.Errorf("upsert embedding: %w", err)
	}

	return nil
}

// BackfillEmbeddings enqueues embedding jobs for the given model for all feedback records that have
// non-empty value_text and no embedding row for that model (existing rows are replaced by upsert when the job runs).
// Returns the number of jobs enqueued. Requires embeddingInserter and embeddingQueueName to be set.
func (s *FeedbackRecordsService) BackfillEmbeddings(ctx context.Context, model string) (int, error) {
	if s.embeddingInserter == nil || s.embeddingQueueName == "" {
		return 0, ErrEmbeddingBackfillNotConfigured
	}

	ids, err := s.embeddingsRepo.ListFeedbackRecordIDsForBackfill(ctx, model)
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
		_, err := s.embeddingInserter.Insert(ctx, FeedbackEmbeddingArgs{
			FeedbackRecordID: id,
			Model:            model,
			ValueTextHash:    "backfill",
		}, opts)
		if err != nil {
			return enqueued, fmt.Errorf("enqueue embedding job for %s: %w", id, err)
		}

		enqueued++
	}

	return enqueued, nil
}
