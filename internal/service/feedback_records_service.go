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
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
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

// FeedbackRecordsRepository defines the interface for feedback records data access.
type FeedbackRecordsRepository interface { //nolint:interfacebloat // one cohesive feedback-record data-access boundary.
	Create(
		ctx context.Context, req *models.CreateFeedbackRecordRequest, hook repository.RecordWriteHook,
	) (*models.FeedbackRecord, error)
	GetByID(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	List(ctx context.Context, filters *models.ListFeedbackRecordsFilters) ([]models.FeedbackRecord, bool, error)
	ListAfterCursor(
		ctx context.Context, filters *models.ListFeedbackRecordsFilters,
		cursorCollectedAt time.Time, cursorID uuid.UUID,
	) ([]models.FeedbackRecord, bool, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest, hook repository.RecordWriteHook,
	) (updated, previous *models.FeedbackRecord, err error)
	SetTranslation(ctx context.Context, feedbackRecordID uuid.UUID, translated *string, langKey, defaultLang string,
		stillCurrent func(valueText *string) bool, hook repository.RecordWriteHook) error
	SetSentiment(ctx context.Context, feedbackRecordID uuid.UUID, sentiment *models.SentimentValue, score *float64,
		stillCurrent func(valueText *string) bool) error
	SetEmotions(ctx context.Context, feedbackRecordID uuid.UUID, emotions []models.EmotionValue,
		stillCurrent func(valueText *string) bool) error
	ClearEmotions(ctx context.Context, feedbackRecordID uuid.UUID,
		stillCurrent func(valueText *string) bool) error
	ListTranslationBackfillTargets(
		ctx context.Context, afterID uuid.UUID, limit int, defaultLang string,
	) ([]models.TranslationBackfillTarget, error)
	ListTranslationBackfillTargetsForTenant(
		ctx context.Context, tenantID string, afterID uuid.UUID, limit int, defaultLang string,
	) ([]models.TranslationBackfillTarget, error)
	ListSentimentBackfillTargets(ctx context.Context, afterID uuid.UUID, limit int) ([]uuid.UUID, error)
	ListEmotionsBackfillTargets(ctx context.Context, afterID uuid.UUID, limit int) ([]uuid.UUID, error)
	Count(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (int, error)
	Delete(ctx context.Context, id uuid.UUID) error
	DeleteByUser(ctx context.Context, filters *models.DeleteFeedbackRecordsByUserFilters) ([]models.DeletedFeedbackRecordsByTenant, error)
}

// EmbeddingsRepository defines the interface for embeddings table access.
type EmbeddingsRepository interface {
	Upsert(
		ctx context.Context, feedbackRecordID uuid.UUID, model string, inputKind models.EmbeddingInputKind,
		embedding []float32, stillCurrent func(fieldLabel, valueText, valueTextTranslated *string) bool,
	) error
	DeleteByFeedbackRecordAndModel(
		ctx context.Context, feedbackRecordID uuid.UUID, model string,
		stillCurrent func(fieldLabel, valueText, valueTextTranslated *string) bool,
	) error
	ListPendingEmbeddings(ctx context.Context, filter repository.PendingEmbeddingsFilter, limit int) ([]uuid.UUID, error)
	ListTenantPendingEmbeddings(
		ctx context.Context, tenantID string, filter repository.PendingEmbeddingsFilter, limit int,
	) ([]uuid.UUID, error)
	CountPendingEmbeddings(ctx context.Context, filter repository.PendingEmbeddingsFilter) (int, error)
	CountTenantPendingEmbeddings(ctx context.Context, tenantID string, filter repository.PendingEmbeddingsFilter) (int, error)
	EmbeddingIsCurrent(
		ctx context.Context, feedbackRecordID uuid.UUID, model string, inputKind models.EmbeddingInputKind,
	) (bool, error)
}

// EnrichmentClearMetrics records enrichment outputs nulled by an edit's eager-clear, labeled by
// output. Optional: nil disables it; set via SetEnrichmentClearMetrics (the clear fires only on
// the API's UpdateFeedbackRecord path).
type EnrichmentClearMetrics interface {
	RecordOutputCleared(ctx context.Context, output string)
}

// FeedbackRecordsService handles business logic for feedback records.
type FeedbackRecordsService struct {
	repo                   FeedbackRecordsRepository
	embeddingsRepo         EmbeddingsRepository
	embeddingModel         string
	publisher              MessagePublisher
	embeddingEnqueuer      *EmbeddingEnqueuer
	embeddingInserter      RiverBatchInserter
	embeddingQueueName     string
	embeddingMaxAttempts   int
	translationDefaultLang string
	clearMetrics           EnrichmentClearMetrics
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
	embeddingInserter RiverBatchInserter,
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

// SetEmbeddingInserter sets the River inserter the embedding backfill uses (e.g. after the River
// client is created).
func (s *FeedbackRecordsService) SetEmbeddingInserter(inserter RiverBatchInserter) {
	s.embeddingInserter = inserter
}

// SetEmbeddingEnqueuer makes record creates, edits and translation writes enqueue the embedding
// jobs they owe inside their own transaction. nil (the default) enqueues none, for processes that
// do not embed.
func (s *FeedbackRecordsService) SetEmbeddingEnqueuer(enqueuer *EmbeddingEnqueuer) {
	s.embeddingEnqueuer = enqueuer
}

// SetEnrichmentClearMetrics enables the eager-clear counter. Wire it on the API service instance
// (the eager-clear fires on UpdateFeedbackRecord); leaving it unset disables the metric.
func (s *FeedbackRecordsService) SetEnrichmentClearMetrics(m EnrichmentClearMetrics) {
	s.clearMetrics = m
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

	// One event ID for the transactional embedding jobs and the published event, so every job and
	// webhook delivery this create causes correlates.
	event := NewEvent(datatypes.FeedbackRecordCreated, nil, nil)

	// A record that owes no embedding (no text: a number, a boolean, a date) keeps the single-
	// statement insert; only one that does pays for the explicit transaction.
	var hook repository.RecordWriteHook
	if s.embeddingEnqueuer != nil && s.embeddingEnqueuer.OwesCreate(normalizedReq.FieldLabel, normalizedReq.ValueText) {
		hook = s.embeddingWriteHook(event, nil)
	}

	record, err := s.repo.Create(ctx, &normalizedReq, hook)
	if err != nil {
		return nil, fmt.Errorf("create feedback record: %w", err)
	}

	if s.publisher != nil {
		event.Data = record
		s.publisher.Publish(ctx, event)
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
// stillCurrent (optional) is the repository's content-supersession guard: it is given the
// record's current value_text atomically with the write, and a false return skips the write
// with huberrors.ErrTranslationSuperseded (nil ⇒ unconditional).
func (s *FeedbackRecordsService) SetTranslation(
	ctx context.Context, feedbackRecordID uuid.UUID, translated *string, langKey string,
	stillCurrent func(valueText *string) bool,
) error {
	// A translation must carry the locale it was produced in; reject an inconsistent
	// (translated, "") pair. Clearing (translated == nil) intentionally passes "".
	if translated != nil && strings.TrimSpace(langKey) == "" {
		return ErrTranslationLangKeyRequired
	}

	var hook repository.RecordWriteHook
	if s.embeddingEnqueuer != nil {
		hook = func(ctx context.Context, dbTx pgx.Tx, _, _ *models.FeedbackRecord) error {
			return s.embeddingEnqueuer.EnqueueTranslationTx(ctx, dbTx, feedbackRecordID)
		}
	}

	if err := s.repo.SetTranslation(
		ctx, feedbackRecordID, translated, langKey, s.translationDefaultLang, stillCurrent, hook,
	); err != nil {
		return fmt.Errorf("set feedback record translation: %w", err)
	}

	return nil
}

// SetSentiment persists or clears the sentiment label and score for a feedback record. It is the
// accessor the sentiment worker uses; the write is tenant-write-locked in the repository and
// publishes no event (no enrichment loop). stillCurrent (optional) is the repository's
// content-supersession guard: it is given the record's current value_text atomically with the
// write, and a false return skips the write with huberrors.ErrClassificationSuperseded (nil ⇒
// unconditional). Passing a nil sentiment clears both columns; a non-nil label must be valid and
// carry a score.
func (s *FeedbackRecordsService) SetSentiment(
	ctx context.Context, feedbackRecordID uuid.UUID, sentiment *models.SentimentValue, score *float64,
	stillCurrent func(valueText *string) bool,
) error {
	// Clearing nulls both columns: a score has no meaning without a label.
	if sentiment == nil {
		if err := s.repo.SetSentiment(ctx, feedbackRecordID, nil, nil, stillCurrent); err != nil {
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

	if err := s.repo.SetSentiment(ctx, feedbackRecordID, sentiment, score, stillCurrent); err != nil {
		return fmt.Errorf("set feedback record sentiment: %w", err)
	}

	return nil
}

// SetEmotions persists or clears the emotion labels for a feedback record. It is the accessor the
// emotion worker uses; the write is tenant-write-locked in the repository and publishes no event
// (no enrichment loop). stillCurrent (optional) is the repository's content-supersession guard:
// it is given the record's current value_text atomically with the write, and a false return skips
// the write with huberrors.ErrClassificationSuperseded (nil ⇒ unconditional). Emotions are
// multi-label; an empty (or nil) set still records a COMPLETED classification (the labels column
// stays NULL, but the completion marker is stamped), which is what distinguishes "no emotion
// detected" from "not yet enriched" -- use ClearEmotions for the latter.
func (s *FeedbackRecordsService) SetEmotions(
	ctx context.Context, feedbackRecordID uuid.UUID, emotions []models.EmotionValue,
	stillCurrent func(valueText *string) bool,
) error {
	// An empty set stores NULL labels (never an empty array) but is still a classification result,
	// so it goes through SetEmotions -- not ClearEmotions -- to stamp the completion marker.
	if len(emotions) == 0 {
		if err := s.repo.SetEmotions(ctx, feedbackRecordID, nil, stillCurrent); err != nil {
			return fmt.Errorf("set empty feedback record emotions: %w", err)
		}

		return nil
	}

	for _, emotion := range emotions {
		if !emotion.IsValid() {
			return ErrInvalidEmotionLabel
		}
	}

	if err := s.repo.SetEmotions(ctx, feedbackRecordID, emotions, stillCurrent); err != nil {
		return fmt.Errorf("set feedback record emotions: %w", err)
	}

	return nil
}

// ClearEmotions returns a record to the "not classified" state, dropping both the labels and the
// completion marker. Used when the source content is gone, so the record is excluded from progress
// counts rather than counting as classified-with-no-emotions.
func (s *FeedbackRecordsService) ClearEmotions(
	ctx context.Context, feedbackRecordID uuid.UUID, stillCurrent func(valueText *string) bool,
) error {
	if err := s.repo.ClearEmotions(ctx, feedbackRecordID, stillCurrent); err != nil {
		return fmt.Errorf("clear feedback record emotions: %w", err)
	}

	return nil
}

// ListFeedbackRecords retrieves a list of feedback records with optional filters.
// Uses cursor-based pagination: omit cursor for first page, use next_cursor for subsequent pages.
//
// The provided *filters is mutated in-place to fill in the default Sort, Order and Limit when
// the caller omits them. This is safe because the HTTP handler (the only caller) constructs a
// fresh struct per request.
func (s *FeedbackRecordsService) ListFeedbackRecords(
	ctx context.Context, filters *models.ListFeedbackRecordsFilters,
) (*models.ListFeedbackRecordsResponse, error) {
	if filters == nil {
		filters = &models.ListFeedbackRecordsFilters{}
	}

	if filters.Limit <= 0 {
		filters.Limit = 100
	}

	// Resolve the ordering once, here, so the keyset predicate, the ORDER BY and the next cursor
	// are all derived from the same values and cannot disagree about what a page is ordered by.
	if filters.Sort == "" {
		filters.Sort = models.DefaultSortField
	}

	if filters.Order == "" {
		filters.Order = models.DefaultSortOrder
	}

	cursorStr := strings.TrimSpace(filters.Cursor)

	var (
		records []models.FeedbackRecord
		hasMore bool
		err     error
	)

	if cursorStr != "" {
		key, decErr := cursor.DecodeKey(cursorStr)
		if decErr != nil {
			return nil, fmt.Errorf("decode cursor: %w", decErr)
		}

		// A cursor bounds one column in one direction. Under a different ordering it still yields
		// a valid-looking page that is not the continuation of the previous one, so refuse it
		// rather than silently skipping and repeating rows.
		//
		// A cursor issued before sort control carries no ordering, but it is not a wildcard: every
		// one of them is a position in the ordering this endpoint had at the time, which is the
		// default. Resolving it to that lets old cursors keep working on the default listing while
		// still refusing to bind a collected_at position to a created_at keyset predicate.
		key = key.ResolveOrdering(string(models.DefaultSortField), string(models.DefaultSortOrder))
		if matchErr := key.Match(string(filters.Sort), string(filters.Order)); matchErr != nil {
			return nil, fmt.Errorf("validate cursor ordering: %w", matchErr)
		}

		records, hasMore, err = s.repo.ListAfterCursor(ctx, filters, key.Timestamp, key.ID)
	} else {
		records, hasMore, err = s.repo.List(ctx, filters)
	}

	if err != nil {
		return nil, fmt.Errorf("list feedback records: %w", err)
	}

	meta, err := BuildListPaginationMeta(filters.Limit, hasMore, func() (string, error) {
		// hasMore is derived from len(records) > limit, so a non-empty page is implied here.
		// Guarded anyway, mirroring the webhooks list path: an out-of-range index would panic in
		// the request goroutine rather than surface as an error.
		if len(records) == 0 {
			return "", ErrPaginationInvariantViolated
		}

		last := records[len(records)-1]

		return cursor.EncodeKey(cursor.Key{
			Timestamp: last.SortValue(filters.Sort),
			ID:        last.ID,
			Sort:      string(filters.Sort),
			Order:     string(filters.Order),
		})
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

// CountFeedbackRecords returns the count of feedback records matching the given filters.
func (s *FeedbackRecordsService) CountFeedbackRecords(
	ctx context.Context, filters *models.ListFeedbackRecordsFilters,
) (int, error) {
	if filters == nil {
		filters = &models.ListFeedbackRecordsFilters{}
	}

	count, err := s.repo.Count(ctx, filters)
	if err != nil {
		return 0, fmt.Errorf("count feedback records: %w", err)
	}

	return count, nil
}

// UpdateFeedbackRecord updates an existing feedback record.
func (s *FeedbackRecordsService) UpdateFeedbackRecord(
	ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	// Update returns the pre-update ("previous") row captured atomically with the write, so the
	// event carries the fields that ACTUALLY changed: an integration idempotently re-PATCHing the
	// same values must not re-fire webhooks or re-run every LLM enrichment, and the diff is
	// computed against state consistent with the write (no pre-lock snapshot race).
	changedFields := func(previous *models.FeedbackRecord) []string {
		if previous == nil {
			return req.ChangedFields()
		}

		return req.FieldsChangedFrom(previous)
	}

	event := NewEvent(datatypes.FeedbackRecordUpdated, nil, nil)

	record, previous, err := s.repo.Update(ctx, id, req, s.embeddingWriteHook(event, changedFields))
	if err != nil {
		return nil, fmt.Errorf("update feedback record: %w", err)
	}

	// A no-op update (no fields set, or every set field equal to its current value) must not
	// publish an "updated" event: firing tenant-owned side effects for a nothing-changed write
	// would re-trigger webhooks and enrichment for free — including while the tenant is under a
	// data purge. The embedding hook applies the same rule inside the transaction.
	if s.publisher != nil {
		if changed := changedFields(previous); len(changed) > 0 {
			event.Data = record
			event.ChangedFields = changed
			s.publisher.Publish(ctx, event)
		}
	}

	// Make the eager-clear observable: which enrichment outputs this edit nulled (derived from
	// the atomic previous/updated pair, so it costs no extra read). A cleared output that never
	// re-enriches is the backfill-recovery case; without this line the clear rate is invisible.
	if cleared := clearedEnrichmentFields(previous, record); len(cleared) > 0 {
		slog.Info("update feedback record: enrichment outputs cleared by content edit",
			"feedback_record_id", id, "tenant_id", record.TenantID, "cleared", cleared)

		if s.clearMetrics != nil {
			for _, output := range cleared {
				s.clearMetrics.RecordOutputCleared(ctx, output)
			}
		}
	}

	return record, nil
}

// clearedEnrichmentFields lists the enrichment outputs the update's eager-clear nulled — present
// on the pre-update row, absent on the updated one.
func clearedEnrichmentFields(previous, updated *models.FeedbackRecord) []string {
	if previous == nil || updated == nil {
		return nil
	}

	var cleared []string

	if previous.Sentiment != nil && updated.Sentiment == nil {
		cleared = append(cleared, "sentiment")
	}

	if previous.Emotions != nil && updated.Emotions == nil {
		cleared = append(cleared, "emotions")
	}

	if previous.ValueTextTranslated != nil && updated.ValueTextTranslated == nil {
		cleared = append(cleared, "translation")
	}

	return cleared
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
	ctx context.Context, feedbackRecordID uuid.UUID, model string, inputKind models.EmbeddingInputKind,
	embedding []float32, stillCurrent func(fieldLabel, valueText, valueTextTranslated *string) bool,
) error {
	if embedding == nil {
		if err := s.embeddingsRepo.DeleteByFeedbackRecordAndModel(ctx, feedbackRecordID, model, stillCurrent); err != nil {
			return fmt.Errorf("delete embedding: %w", err)
		}

		return nil
	}

	if err := s.embeddingsRepo.Upsert(ctx, feedbackRecordID, model, inputKind, embedding, stillCurrent); err != nil {
		return fmt.Errorf("upsert embedding: %w", err)
	}

	return nil
}

// embeddingBackfillPageSize bounds how many record ids the embedding backfill lists and
// enqueues per keyset page, so a large deployment is never fully materialized in memory.
const embeddingBackfillPageSize = 500

// EmbeddingBackfillParams scopes an operator backfill (backfill-embeddings).
type EmbeddingBackfillParams struct {
	Model     string
	InputKind models.EmbeddingInputKind
	// TenantID restricts the backfill to one tenant (a feedback directory); empty is deployment-wide.
	TenantID string
	// MaxRecords stops after this many selected records; zero is unlimited.
	MaxRecords int
	// IncludeLegacy also re-embeds vectors written before Hub recorded what they were computed
	// from. Tenant-scoped only: it can select every vector a tenant has.
	IncludeLegacy bool
	// IncludeTerminal also retries records the provider rejected outright, for after the reason
	// was fixed (a lower input cap, say). Tenant-scoped only.
	IncludeTerminal bool
	// Cooldown skips records whose attempts ran out on a transient error less than this long ago.
	// Zero (the default) retries them at once, which is what recovery after a provider outage
	// wants; a longer one keeps a record that keeps failing out of repeated batch runs.
	Cooldown time.Duration
}

// EmbeddingBackfillResult reports what a backfill did.
type EmbeddingBackfillResult struct {
	// Selected is how many records the backfill found owed work.
	Selected int
	// Enqueued is how many jobs it queued; Selected minus Enqueued were already queued.
	Enqueued int
}

// filter is the selection a backfill makes. Unlike the sweep it has no transient-failure cooldown
// by default: an operator runs it once the provider has recovered, and waiting out the sweep's
// cooldown then would hide exactly the records the incident left behind. Re-runs stay idempotent
// through the in-flight dedupe.
func (p EmbeddingBackfillParams) filter(now time.Time) repository.PendingEmbeddingsFilter {
	return repository.PendingEmbeddingsFilter{
		Model:           p.Model,
		InputKind:       models.NormalizeEmbeddingInputKind(p.InputKind),
		RetryBefore:     now.Add(-p.Cooldown),
		IncludeLegacy:   p.IncludeLegacy,
		IncludeTerminal: p.IncludeTerminal,
	}
}

func (p EmbeddingBackfillParams) validate() error {
	if (p.IncludeLegacy || p.IncludeTerminal) && p.TenantID == "" {
		return ErrMissingTenantID
	}

	return nil
}

// CountEmbeddingBackfill counts what BackfillEmbeddings would select (before MaxRecords).
func (s *FeedbackRecordsService) CountEmbeddingBackfill(ctx context.Context, params EmbeddingBackfillParams) (int, error) {
	if err := params.validate(); err != nil {
		return 0, err
	}

	filter := params.filter(time.Now())

	var (
		count int
		err   error
	)

	if params.TenantID == "" {
		count, err = s.embeddingsRepo.CountPendingEmbeddings(ctx, filter)
	} else {
		count, err = s.embeddingsRepo.CountTenantPendingEmbeddings(ctx, params.TenantID, filter)
	}

	if err != nil {
		return 0, fmt.Errorf("count embedding backfill: %w", err)
	}

	return count, nil
}

// BackfillEmbeddings queues an embedding job for every record owed one under params, on the live
// embeddings queue so incident recovery runs at full worker concurrency. It selects what the repair
// sweep would (repository.PendingEmbeddingsFilter): missing, stale and orphaned vectors, skipping
// records with a job already in flight on any queue and terminal failures, but not waiting out the
// transient-failure cooldown (see filter). Its jobs share the sweep's unique key, so the two never
// queue the same repair twice. One pass in id-ordered keyset pages, so the full set is never
// materialized and records whose job finishes mid-run are not visited twice.
func (s *FeedbackRecordsService) BackfillEmbeddings(
	ctx context.Context, params EmbeddingBackfillParams,
) (EmbeddingBackfillResult, error) {
	result := EmbeddingBackfillResult{}

	if s.embeddingInserter == nil || s.embeddingQueueName == "" {
		return result, ErrEmbeddingBackfillNotConfigured
	}

	if err := params.validate(); err != nil {
		return result, err
	}

	filter := params.filter(time.Now())

	for {
		pageSize := embeddingBackfillPageSize
		if params.MaxRecords > 0 {
			pageSize = min(pageSize, params.MaxRecords-result.Selected)
		}

		if pageSize <= 0 {
			break
		}

		page, err := s.listEmbeddingBackfillPage(ctx, params.TenantID, filter, pageSize)
		if err != nil {
			return result, err
		}

		if len(page) == 0 {
			break
		}

		target := EmbeddingReconcileTarget{Model: filter.Model, InputKind: filter.InputKind}

		jobs := make([]river.InsertManyParams, 0, len(page))
		for _, id := range page {
			jobs = append(jobs, repairEmbeddingJob(id, target, s.embeddingQueueName, 0, s.embeddingMaxAttempts))
		}

		inserted, err := s.embeddingInserter.InsertMany(ctx, jobs)
		if err != nil {
			return result, fmt.Errorf("enqueue embedding backfill: %w", err)
		}

		result.Selected += len(page)

		for _, job := range inserted {
			if job != nil && !job.UniqueSkippedAsDuplicate {
				result.Enqueued++
			}
		}

		filter.AfterID = page[len(page)-1]

		if len(page) < pageSize {
			break
		}
	}

	return result, nil
}

//nolint:funcorder // scoped helper stays with the public backfill entry points
func (s *FeedbackRecordsService) listEmbeddingBackfillPage(
	ctx context.Context, tenantID string, filter repository.PendingEmbeddingsFilter, limit int,
) ([]uuid.UUID, error) {
	var (
		page []uuid.UUID
		err  error
	)

	if tenantID == "" {
		page, err = s.embeddingsRepo.ListPendingEmbeddings(ctx, filter, limit)
	} else {
		page, err = s.embeddingsRepo.ListTenantPendingEmbeddings(ctx, tenantID, filter, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("list records for embedding backfill: %w", err)
	}

	return page, nil
}

// EmbeddingIsCurrent reports whether the record already has a vector for model built from its
// current content (internal use by the embeddings worker, to skip a provider call).
func (s *FeedbackRecordsService) EmbeddingIsCurrent(
	ctx context.Context, feedbackRecordID uuid.UUID, model string, inputKind models.EmbeddingInputKind,
) (bool, error) {
	current, err := s.embeddingsRepo.EmbeddingIsCurrent(ctx, feedbackRecordID, model, inputKind)
	if err != nil {
		return false, fmt.Errorf("check embedding is current: %w", err)
	}

	return current, nil
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
// one-off global backfill command. runID discriminates this run's jobs from earlier runs'
// (see enqueueTranslationBackfillJobs). Returns the number of jobs enqueued.
func (s *FeedbackRecordsService) BackfillTranslations(
	ctx context.Context, inserter RiverJobInserter, queueName string, maxAttempts int, runID string,
) (int, error) {
	return s.backfillTranslationsPaged(ctx, inserter, translationBackfillInsertOpts(queueName, maxAttempts), runID,
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
// (TenantTranslationBackfillArgs). runID discriminates this run's jobs from earlier runs'
// (see enqueueTranslationBackfillJobs). Returns the number of jobs enqueued.
func (s *FeedbackRecordsService) BackfillTranslationsForTenant(
	ctx context.Context, inserter RiverJobInserter, queueName string, maxAttempts int, tenantID, runID string,
) (int, error) {
	return s.backfillTranslationsPaged(ctx, inserter, translationBackfillInsertOpts(queueName, maxAttempts), runID,
		func(afterID uuid.UUID) ([]models.TranslationBackfillTarget, error) {
			targets, err := s.repo.ListTranslationBackfillTargetsForTenant(
				ctx, tenantID, afterID, translationBackfillPageSize, s.translationDefaultLang)
			if err != nil {
				return nil, fmt.Errorf("list translation backfill targets for tenant %s: %w", tenantID, err)
			}

			return targets, nil
		})
}

// embeddingWriteHook returns the hook that enqueues event's embedding jobs inside the write's
// transaction, or nil when this process does not embed. changedFields derives the update's changed
// fields from the row the write replaced; nil for a create.
func (s *FeedbackRecordsService) embeddingWriteHook(
	event Event, changedFields func(previous *models.FeedbackRecord) []string,
) repository.RecordWriteHook {
	if s.embeddingEnqueuer == nil {
		return nil
	}

	return func(ctx context.Context, dbTx pgx.Tx, written, previous *models.FeedbackRecord) error {
		owed := event
		owed.Data = written

		if changedFields != nil {
			owed.ChangedFields = changedFields(previous)
			if len(owed.ChangedFields) == 0 {
				return nil
			}
		}

		return s.embeddingEnqueuer.EnqueueTx(ctx, dbTx, owed)
	}
}

// backfillTranslationsPaged enqueues a translation job for every target produced by
// fetchPage, streaming in keyset pages (so the full set is never materialized) and stopping
// on the first short page. Advancing the cursor by the last id seen means even a
// fully-deduped page cannot livelock the loop.
func (s *FeedbackRecordsService) backfillTranslationsPaged(
	ctx context.Context,
	inserter RiverJobInserter,
	opts *river.InsertOpts,
	runID string,
	fetchPage func(afterID uuid.UUID) ([]models.TranslationBackfillTarget, error),
) (int, error) {
	return backfillPaged("translation", translationBackfillPageSize, fetchPage,
		func(target models.TranslationBackfillTarget) uuid.UUID { return target.FeedbackRecordID },
		func(targets []models.TranslationBackfillTarget) (int, int, error) {
			return enqueueTranslationBackfillJobs(ctx, inserter, opts, runID, targets)
		})
}

// translationBackfillInsertOpts is the shared River insert config for backfill translation
// jobs: per-record dedup by (record, target, run) within the 24h window, so a rescued or
// retried fan-out cannot double-enqueue the pages it already inserted.
func translationBackfillInsertOpts(queueName string, maxAttempts int) *river.InsertOpts {
	return &river.InsertOpts{
		Queue:       queueName,
		MaxAttempts: maxAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: uniqueByPeriodTranslation},
	}
}

// enqueueTranslationBackfillJobs inserts one FeedbackTranslationArgs per target. The
// "backfill:<runID>" hash marks these jobs distinct from event-driven ones AND from earlier
// runs' jobs: River's unique states include completed, so a constant marker would let a
// completed job from a previous run (e.g. before the tenant's target flip-flopped back the
// same day) silently swallow this run's re-translation. Within one run the marker is stable,
// so a rescued/retried fan-out still dedupes its own re-inserted pages. Unique-skipped
// duplicates are counted separately — never as enqueued. Stops on the first insert error.
func enqueueTranslationBackfillJobs(
	ctx context.Context, inserter RiverJobInserter, opts *river.InsertOpts,
	runID string, targets []models.TranslationBackfillTarget,
) (enqueued, skipped int, err error) {
	for _, target := range targets {
		res, err := inserter.Insert(ctx, FeedbackTranslationArgs{
			FeedbackRecordID: target.FeedbackRecordID,
			TargetLang:       target.TargetLang,
			ValueTextHash:    "backfill:" + runID,
		}, opts)
		if err != nil {
			return enqueued, skipped, fmt.Errorf("enqueue translation job for %s: %w", target.FeedbackRecordID, err)
		}

		if res != nil && res.UniqueSkippedAsDuplicate {
			skipped++

			continue
		}

		enqueued++
	}

	return enqueued, skipped, nil
}
