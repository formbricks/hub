// Package service implements business logic for feedback records.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/cursor"
)

// ErrUserIDRequired is returned when deleting feedback records by user is called without user_id.
var ErrUserIDRequired = huberrors.NewValidationError("user_id", "user_id is required")

// ErrEmbeddingBackfillNotConfigured is returned when BackfillEmbeddings is called without embedding inserter/queue.
var ErrEmbeddingBackfillNotConfigured = errors.New("embedding backfill not configured")

// ErrTranslationLangKeyRequired is returned when a translation is set without a target
// locale key: a translation must record the locale it was produced in (clearing, where
// translated is nil, intentionally passes an empty key to null both columns).
var ErrTranslationLangKeyRequired = errors.New("translation lang key is required when translated text is set")

// ErrInvalidSentimentLabel is returned when SetSentiment is given an unknown sentiment label.
var ErrInvalidSentimentLabel = errors.New("invalid sentiment label")

// ErrSentimentScoreRequired is returned when a sentiment label is set without a score: a label
// must carry its score (clearing, where sentiment is nil, nulls both columns).
var ErrSentimentScoreRequired = errors.New("sentiment score is required when a label is set")

// ErrInvalidEmotionLabel is returned when SetEmotions is given an unknown emotion label.
var ErrInvalidEmotionLabel = errors.New("invalid emotion label")

const uniqueByPeriodEmbedding = 24 * time.Hour

// FeedbackRecordsRepository defines the interface for feedback records data access.
type FeedbackRecordsRepository interface { //nolint:interfacebloat // one cohesive feedback-record data-access boundary.
	Create(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	GetByID(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	List(ctx context.Context, filters *models.ListFeedbackRecordsFilters) ([]models.FeedbackRecord, bool, error)
	ListAfterCursor(
		ctx context.Context, filters *models.ListFeedbackRecordsFilters,
		cursorCollectedAt time.Time, cursorID uuid.UUID,
	) ([]models.FeedbackRecord, bool, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	SetTranslation(ctx context.Context, feedbackRecordID uuid.UUID, translated *string, langKey, defaultLang string) error
	SetSentiment(ctx context.Context, feedbackRecordID uuid.UUID, sentiment *models.SentimentValue, score *float64) error
	SetEmotions(ctx context.Context, feedbackRecordID uuid.UUID, emotions []models.EmotionValue) error
	ListTranslationBackfillTargets(
		ctx context.Context, afterID uuid.UUID, limit int, defaultLang string,
	) ([]models.TranslationBackfillTarget, error)
	ListTranslationBackfillTargetsForTenant(
		ctx context.Context, tenantID string, afterID uuid.UUID, limit int, defaultLang string,
	) ([]models.TranslationBackfillTarget, error)
	Delete(ctx context.Context, id uuid.UUID) error
	DeleteByUser(ctx context.Context, filters *models.DeleteFeedbackRecordsByUserFilters) ([]models.DeletedFeedbackRecordsByTenant, error)
}

// EmbeddingsRepository defines the interface for embeddings table access.
type EmbeddingsRepository interface {
	Upsert(ctx context.Context, feedbackRecordID uuid.UUID, model string, embedding []float32) error
	DeleteByFeedbackRecordAndModel(ctx context.Context, feedbackRecordID uuid.UUID, model string) error
	ListFeedbackRecordIDsForBackfill(
		ctx context.Context, model string, afterID uuid.UUID, limit int,
	) ([]uuid.UUID, error)
}

// FeedbackRecordsService handles business logic for feedback records.
type FeedbackRecordsService struct {
	repo                   FeedbackRecordsRepository
	embeddingsRepo         EmbeddingsRepository
	embeddingModel         string
	publisher              MessagePublisher
	embeddingInserter      RiverJobInserter
	embeddingQueueName     string
	embeddingMaxAttempts   int
	translationDefaultLang string
}

// NewFeedbackRecordsService creates a new feedback records service.
// publisher may be nil when the service is used only for backfill (BackfillEmbeddings does not use the publisher).
// embeddingInserter and embeddingQueueName are optional (for backfill); when nil/empty, BackfillEmbeddings returns an error.
// Call SetEmbeddingInserter after the River client is created to enable backfill without building the service twice.
// embeddingsRepo and embeddingModel are required for SetEmbedding and BackfillEmbeddings (from EMBEDDING_PROVIDER and EMBEDDING_MODEL).
// translationDefaultLang (TRANSLATION_DEFAULT_LANGUAGE) is the fallback target for tenants with no
// target_language of their own; "" disables the fallback. It governs the SetTranslation write-guard
// and both translation backfills, so pass it wherever translation work runs.
func NewFeedbackRecordsService(
	repo FeedbackRecordsRepository,
	embeddingsRepo EmbeddingsRepository,
	embeddingModel string,
	publisher MessagePublisher,
	embeddingInserter RiverJobInserter,
	embeddingQueueName string,
	embeddingMaxAttempts int,
	translationDefaultLang string,
) *FeedbackRecordsService {
	return &FeedbackRecordsService{
		repo:                   repo,
		embeddingsRepo:         embeddingsRepo,
		embeddingModel:         embeddingModel,
		publisher:              publisher,
		embeddingInserter:      embeddingInserter,
		embeddingQueueName:     embeddingQueueName,
		embeddingMaxAttempts:   embeddingMaxAttempts,
		translationDefaultLang: translationDefaultLang,
	}
}

// SetEmbeddingInserter sets the River inserter for embedding jobs (e.g. after River client is created).
// This allows a single service instance to be used by both handlers and the embedding worker.
func (s *FeedbackRecordsService) SetEmbeddingInserter(inserter RiverJobInserter) {
	s.embeddingInserter = inserter
}

// CreateFeedbackRecord creates a new feedback record.
func (s *FeedbackRecordsService) CreateFeedbackRecord(
	ctx context.Context, req *models.CreateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(req.TenantID)
	if err != nil {
		return nil, err
	}

	normalizedReq := *req
	normalizedReq.TenantID = normalizedTenantID

	record, err := s.repo.Create(ctx, &normalizedReq)
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

// SetTranslation persists the translated value_text and the target locale key for a
// feedback record. It is the accessor the translation worker uses; the write is
// tenant-write-locked in the repository and publishes no event (no enrichment loop).
func (s *FeedbackRecordsService) SetTranslation(
	ctx context.Context, feedbackRecordID uuid.UUID, translated *string, langKey string,
) error {
	// A translation must carry the locale it was produced in; reject an inconsistent
	// (translated, "") pair. Clearing (translated == nil) intentionally passes "".
	if translated != nil && strings.TrimSpace(langKey) == "" {
		return ErrTranslationLangKeyRequired
	}

	if err := s.repo.SetTranslation(ctx, feedbackRecordID, translated, langKey, s.translationDefaultLang); err != nil {
		return fmt.Errorf("set feedback record translation: %w", err)
	}

	return nil
}

// SetSentiment persists or clears the sentiment label and score for a feedback record. It is the
// accessor the sentiment worker uses; the write is tenant-write-locked in the repository and
// publishes no event (no enrichment loop). Passing a nil sentiment clears both columns; a
// non-nil label must be valid and carry a score.
func (s *FeedbackRecordsService) SetSentiment(
	ctx context.Context, feedbackRecordID uuid.UUID, sentiment *models.SentimentValue, score *float64,
) error {
	// Clearing nulls both columns: a score has no meaning without a label.
	if sentiment == nil {
		if err := s.repo.SetSentiment(ctx, feedbackRecordID, nil, nil); err != nil {
			return fmt.Errorf("clear feedback record sentiment: %w", err)
		}

		return nil
	}

	if !sentiment.IsValid() {
		return ErrInvalidSentimentLabel
	}

	if score == nil {
		return ErrSentimentScoreRequired
	}

	if err := s.repo.SetSentiment(ctx, feedbackRecordID, sentiment, score); err != nil {
		return fmt.Errorf("set feedback record sentiment: %w", err)
	}

	return nil
}

// SetEmotions persists or clears the emotion labels for a feedback record. It is the accessor the
// emotion worker uses; the write is tenant-write-locked in the repository and publishes no event
// (no enrichment loop). Emotions are multi-label; an empty (or nil) set clears the column, so "no
// emotion detected" and "not yet enriched" share the same NULL representation.
func (s *FeedbackRecordsService) SetEmotions(
	ctx context.Context, feedbackRecordID uuid.UUID, emotions []models.EmotionValue,
) error {
	// An empty set clears (stored as NULL, never an empty array).
	if len(emotions) == 0 {
		if err := s.repo.SetEmotions(ctx, feedbackRecordID, nil); err != nil {
			return fmt.Errorf("clear feedback record emotions: %w", err)
		}

		return nil
	}

	for _, emotion := range emotions {
		if !emotion.IsValid() {
			return ErrInvalidEmotionLabel
		}
	}

	if err := s.repo.SetEmotions(ctx, feedbackRecordID, emotions); err != nil {
		return fmt.Errorf("set feedback record emotions: %w", err)
	}

	return nil
}

// ListFeedbackRecords retrieves a list of feedback records with optional filters.
// Uses cursor-based pagination: omit cursor for first page, use next_cursor for subsequent pages.
func (s *FeedbackRecordsService) ListFeedbackRecords(
	ctx context.Context, filters *models.ListFeedbackRecordsFilters,
) (*models.ListFeedbackRecordsResponse, error) {
	if filters == nil {
		filters = &models.ListFeedbackRecordsFilters{}
	}

	if filters.Limit <= 0 {
		filters.Limit = 100
	}

	cursorStr := strings.TrimSpace(filters.Cursor)

	var (
		records []models.FeedbackRecord
		hasMore bool
		err     error
	)

	if cursorStr != "" {
		collectedAt, id, decErr := cursor.Decode(cursorStr)
		if decErr != nil {
			return nil, fmt.Errorf("decode cursor: %w", decErr)
		}

		records, hasMore, err = s.repo.ListAfterCursor(ctx, filters, collectedAt, id)
	} else {
		records, hasMore, err = s.repo.List(ctx, filters)
	}

	if err != nil {
		return nil, fmt.Errorf("list feedback records: %w", err)
	}

	meta, err := BuildListPaginationMeta(filters.Limit, hasMore, func() (string, error) {
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

	// A no-op update (no fields set) writes nothing — the repository returns the
	// current row without taking the tenant write lock — so it must not publish
	// an "updated" event either. Otherwise an empty PATCH would fire tenant-owned
	// side effects, including while the tenant is under a data purge.
	if changed := req.ChangedFields(); s.publisher != nil && len(changed) > 0 {
		s.publisher.PublishEventWithChangedFields(ctx, datatypes.FeedbackRecordUpdated, record, changed)
	}

	return record, nil
}

// DeleteFeedbackRecord deletes a feedback record by ID.
// Publishes FeedbackRecordDeleted with tenant-aware deleted IDs for webhook isolation.
func (s *FeedbackRecordsService) DeleteFeedbackRecord(ctx context.Context, id uuid.UUID) error {
	record, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get feedback record before delete: %w", err)
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete feedback record: %w", err)
	}

	if s.publisher != nil {
		s.publisher.PublishEvent(ctx, datatypes.FeedbackRecordDeleted, models.DeletedIDsEventData{
			TenantID: record.TenantID,
			IDs:      []uuid.UUID{id},
		})
	}

	return nil
}

// DeleteFeedbackRecordsByUser deletes all feedback records matching user_id.
// When tenant_id is provided, deletion is restricted to that tenant; otherwise all user records are deleted.
// It publishes one tenant-aware FeedbackRecordDeleted event per tenant represented in the deleted rows.
func (s *FeedbackRecordsService) DeleteFeedbackRecordsByUser(
	ctx context.Context, filters *models.DeleteFeedbackRecordsByUserFilters,
) (int, error) {
	if filters == nil {
		return 0, ErrUserIDRequired
	}

	normalizedUserID, err := normalizeRequiredUserIDValue(filters.UserID)
	if err != nil {
		return 0, err
	}

	normalizedFilters := &models.DeleteFeedbackRecordsByUserFilters{
		UserID: normalizedUserID,
	}

	if filters.TenantID != nil {
		normalizedTenantID, err := normalizeRequiredTenantID(filters.TenantID)
		if err != nil {
			return 0, err
		}

		normalizedFilters.TenantID = &normalizedTenantID
	}

	groups, err := s.repo.DeleteByUser(ctx, normalizedFilters)
	if err != nil {
		return 0, fmt.Errorf("delete feedback records by user: %w", err)
	}

	deletedCount := 0
	for _, group := range groups {
		deletedCount += len(group.IDs)

		if len(group.IDs) == 0 || s.publisher == nil {
			continue
		}

		if group.TenantID == "" {
			slog.Error("delete feedback records by user: deleted rows missing tenant_id; skipping webhook event",
				"deleted_count", len(group.IDs),
			)

			continue
		}

		s.publisher.PublishEvent(ctx, datatypes.FeedbackRecordDeleted, models.DeletedIDsEventData(group))
	}

	return deletedCount, nil
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

// embeddingBackfillPageSize bounds how many record ids the embedding backfill lists and
// enqueues per keyset page, so a large deployment is never fully materialized in memory.
const embeddingBackfillPageSize = 500

// BackfillEmbeddings enqueues embedding jobs for the given model for all feedback records that have
// non-empty value_text and no embedding row for that model (existing rows are replaced by upsert when the job runs).
// It streams the records in keyset pages. Returns the number of jobs enqueued. Requires embeddingInserter
// and embeddingQueueName to be set.
func (s *FeedbackRecordsService) BackfillEmbeddings(ctx context.Context, model string) (int, error) {
	if s.embeddingInserter == nil || s.embeddingQueueName == "" {
		return 0, ErrEmbeddingBackfillNotConfigured
	}

	opts := &river.InsertOpts{
		Queue:       s.embeddingQueueName,
		MaxAttempts: s.embeddingMaxAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: uniqueByPeriodEmbedding},
	}

	enqueued := 0
	afterID := uuid.Nil

	for {
		ids, err := s.embeddingsRepo.ListFeedbackRecordIDsForBackfill(ctx, model, afterID, embeddingBackfillPageSize)
		if err != nil {
			return enqueued, fmt.Errorf("list ids for embedding backfill: %w", err)
		}

		if len(ids) == 0 {
			break
		}

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

		// Advance the keyset cursor past the last id seen; the query excludes
		// already-embedded records, so the cursor always moves forward.
		afterID = ids[len(ids)-1]

		if len(ids) < embeddingBackfillPageSize {
			break
		}
	}

	return enqueued, nil
}

// BackfillTranslations enqueues a translation job for every feedback record that needs
// (re)translation to its tenant's configured target language (text records with non-empty
// value_text whose translation is missing or stale). The worker re-resolves the record at
// run time; the "backfill" hash marks these jobs distinct from event-driven ones. The
// inserter, queue, and max attempts come from the caller (a one-off command), so the shared
// service holds no backfill-only dependency. Returns the number of jobs enqueued.
// translationBackfillPageSize bounds how many stale records a backfill lists and enqueues
// per keyset page, so neither the global nor the per-tenant backfill materializes the full
// result set in memory.
const translationBackfillPageSize = 500

// BackfillTranslations enqueues a translation job for every feedback record (across all
// tenants) that needs (re)translation, streaming the targets in keyset pages. Used by the
// one-off global backfill command. Returns the number of jobs enqueued.
func (s *FeedbackRecordsService) BackfillTranslations(
	ctx context.Context, inserter RiverJobInserter, queueName string, maxAttempts int,
) (int, error) {
	return s.backfillTranslationsPaged(ctx, inserter, translationBackfillInsertOpts(queueName, maxAttempts),
		func(afterID uuid.UUID) ([]models.TranslationBackfillTarget, error) {
			targets, err := s.repo.ListTranslationBackfillTargets(
				ctx, afterID, translationBackfillPageSize, s.translationDefaultLang)
			if err != nil {
				return nil, fmt.Errorf("list translation backfill targets: %w", err)
			}

			return targets, nil
		})
}

// BackfillTranslationsForTenant enqueues a translation job for every record of a single
// tenant that needs (re)translation, streaming in keyset pages so a large tenant is never
// fully materialized. It is the bulk work behind a settings-change re-translation
// (TenantTranslationBackfillArgs). Returns the number of jobs enqueued.
func (s *FeedbackRecordsService) BackfillTranslationsForTenant(
	ctx context.Context, inserter RiverJobInserter, queueName string, maxAttempts int, tenantID string,
) (int, error) {
	return s.backfillTranslationsPaged(ctx, inserter, translationBackfillInsertOpts(queueName, maxAttempts),
		func(afterID uuid.UUID) ([]models.TranslationBackfillTarget, error) {
			targets, err := s.repo.ListTranslationBackfillTargetsForTenant(
				ctx, tenantID, afterID, translationBackfillPageSize, s.translationDefaultLang)
			if err != nil {
				return nil, fmt.Errorf("list translation backfill targets for tenant %s: %w", tenantID, err)
			}

			return targets, nil
		})
}

// backfillTranslationsPaged enqueues a translation job for every target produced by
// fetchPage, streaming in keyset pages (so the full set is never materialized) and stopping
// on the first short page. Advancing the cursor by the last id seen means even a
// fully-deduped page cannot livelock the loop.
func (s *FeedbackRecordsService) backfillTranslationsPaged(
	ctx context.Context,
	inserter RiverJobInserter,
	opts *river.InsertOpts,
	fetchPage func(afterID uuid.UUID) ([]models.TranslationBackfillTarget, error),
) (int, error) {
	enqueued := 0
	afterID := uuid.Nil

	for {
		targets, err := fetchPage(afterID)
		if err != nil {
			return enqueued, err
		}

		if len(targets) == 0 {
			break
		}

		inserted, err := enqueueTranslationBackfillJobs(ctx, inserter, opts, targets)
		enqueued += inserted

		if err != nil {
			return enqueued, err
		}

		afterID = targets[len(targets)-1].FeedbackRecordID

		if len(targets) < translationBackfillPageSize {
			break
		}
	}

	return enqueued, nil
}

// translationBackfillInsertOpts is the shared River insert config for backfill translation
// jobs: per-record dedup by (record, target, hash) within the 24h window.
func translationBackfillInsertOpts(queueName string, maxAttempts int) *river.InsertOpts {
	return &river.InsertOpts{
		Queue:       queueName,
		MaxAttempts: maxAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: uniqueByPeriodTranslation},
	}
}

// enqueueTranslationBackfillJobs inserts one FeedbackTranslationArgs per target. The
// "backfill" hash marks these jobs distinct from event-driven ones. It returns the count
// enqueued and stops on the first insert error.
func enqueueTranslationBackfillJobs(
	ctx context.Context, inserter RiverJobInserter, opts *river.InsertOpts,
	targets []models.TranslationBackfillTarget,
) (int, error) {
	enqueued := 0

	for _, target := range targets {
		if _, err := inserter.Insert(ctx, FeedbackTranslationArgs{
			FeedbackRecordID: target.FeedbackRecordID,
			TargetLang:       target.TargetLang,
			ValueTextHash:    "backfill",
		}, opts); err != nil {
			return enqueued, fmt.Errorf("enqueue translation job for %s: %w", target.FeedbackRecordID, err)
		}

		enqueued++
	}

	return enqueued, nil
}
