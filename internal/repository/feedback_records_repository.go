package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// FeedbackRecordsRepository handles data access for feedback records
type FeedbackRecordsRepository struct {
	db *pgxpool.Pool
}

// NewFeedbackRecordsRepository creates a new feedback records repository
func NewFeedbackRecordsRepository(db *pgxpool.Pool) *FeedbackRecordsRepository {
	return &FeedbackRecordsRepository{db: db}
}

// Create inserts a new feedback record
func (r *FeedbackRecordsRepository) Create(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	collectedAt := time.Now()
	if req.CollectedAt != nil {
		collectedAt = *req.CollectedAt
	}

	query := `
		INSERT INTO feedback_records (
			collected_at, source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_identifier, tenant_id, response_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_identifier, tenant_id, response_id
	`

	var record models.FeedbackRecord
	err := r.db.QueryRow(ctx, query,
		collectedAt, req.SourceType, req.SourceID, req.SourceName,
		req.FieldID, req.FieldLabel, req.FieldType,
		req.ValueText, req.ValueNumber, req.ValueBoolean, req.ValueDate,
		req.Metadata, req.Language, req.UserIdentifier, req.TenantID, req.ResponseID,
	).Scan(
		&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.SourceType, &record.SourceID, &record.SourceName,
		&record.FieldID, &record.FieldLabel, &record.FieldType,
		&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
		&record.Metadata, &record.Language, &record.UserIdentifier, &record.TenantID, &record.ResponseID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create feedback record: %w", err)
	}

	return &record, nil
}

// GetByID retrieves a single feedback record by ID
func (r *FeedbackRecordsRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error) {
	query := `
		SELECT id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_identifier, tenant_id, response_id
		FROM feedback_records
		WHERE id = $1
	`

	var record models.FeedbackRecord
	err := r.db.QueryRow(ctx, query, id).Scan(
		&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.SourceType, &record.SourceID, &record.SourceName,
		&record.FieldID, &record.FieldLabel, &record.FieldType,
		&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
		&record.Metadata, &record.Language, &record.UserIdentifier, &record.TenantID, &record.ResponseID,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.NewNotFoundError("feedback record", "feedback record not found")
		}
		return nil, fmt.Errorf("failed to get feedback record: %w", err)
	}

	return &record, nil
}

// buildFilterConditions builds WHERE clause conditions and arguments from filters
// Returns the WHERE clause (including " WHERE " prefix if conditions exist) and the args slice
func buildFilterConditions(filters *models.ListFeedbackRecordsFilters) (string, []interface{}) {
	var conditions []string
	var args []interface{}
	argCount := 1

	if filters.TenantID != nil {
		conditions = append(conditions, fmt.Sprintf("tenant_id = $%d", argCount))
		args = append(args, *filters.TenantID)
		argCount++
	}

	if filters.ResponseID != nil {
		conditions = append(conditions, fmt.Sprintf("response_id = $%d", argCount))
		args = append(args, *filters.ResponseID)
		argCount++
	}

	if filters.SourceType != nil {
		conditions = append(conditions, fmt.Sprintf("source_type = $%d", argCount))
		args = append(args, *filters.SourceType)
		argCount++
	}

	if filters.SourceID != nil {
		conditions = append(conditions, fmt.Sprintf("source_id = $%d", argCount))
		args = append(args, *filters.SourceID)
		argCount++
	}

	if filters.FieldID != nil {
		conditions = append(conditions, fmt.Sprintf("field_id = $%d", argCount))
		args = append(args, *filters.FieldID)
		argCount++
	}

	if filters.FieldType != nil {
		conditions = append(conditions, fmt.Sprintf("field_type = $%d", argCount))
		args = append(args, *filters.FieldType)
		argCount++
	}

	if filters.UserIdentifier != nil {
		conditions = append(conditions, fmt.Sprintf("user_identifier = $%d", argCount))
		args = append(args, *filters.UserIdentifier)
		argCount++
	}

	// Note: TopicID is handled separately via vector similarity search, not here

	if filters.Since != nil {
		conditions = append(conditions, fmt.Sprintf("collected_at >= $%d", argCount))
		args = append(args, *filters.Since)
		argCount++
	}

	if filters.Until != nil {
		conditions = append(conditions, fmt.Sprintf("collected_at <= $%d", argCount))
		args = append(args, *filters.Until)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	return whereClause, args
}

// List retrieves feedback records with optional filters (non-vector based)
func (r *FeedbackRecordsRepository) List(ctx context.Context, filters *models.ListFeedbackRecordsFilters) ([]models.FeedbackRecord, error) {
	query := `
		SELECT id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_identifier, tenant_id, response_id
		FROM feedback_records
	`

	whereClause, args := buildFilterConditions(filters)
	query += whereClause
	argCount := len(args) + 1

	query += " ORDER BY collected_at DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, filters.Limit)
		argCount++
	}

	if filters.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argCount)
		args = append(args, filters.Offset)
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list feedback records: %w", err)
	}
	defer rows.Close()

	records := []models.FeedbackRecord{} // Initialize as empty slice, not nil
	for rows.Next() {
		var record models.FeedbackRecord
		err := rows.Scan(
			&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
			&record.SourceType, &record.SourceID, &record.SourceName,
			&record.FieldID, &record.FieldLabel, &record.FieldType,
			&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
			&record.Metadata, &record.Language, &record.UserIdentifier, &record.TenantID, &record.ResponseID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan feedback record: %w", err)
		}
		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating feedback records: %w", err)
	}

	return records, nil
}

// Count returns the total count of feedback records matching the filters
func (r *FeedbackRecordsRepository) Count(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (int64, error) {
	query := `SELECT COUNT(*) FROM feedback_records`

	whereClause, args := buildFilterConditions(filters)
	query += whereClause

	var count int64
	err := r.db.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count feedback records: %w", err)
	}

	return count, nil
}

// buildUpdateQuery builds an UPDATE query with SET clause and arguments
// Returns the query string, arguments, and a boolean indicating if any updates were provided
func buildUpdateQuery(req *models.UpdateFeedbackRecordRequest, id uuid.UUID, updatedAt time.Time) (string, []interface{}, bool) {
	var updates []string
	var args []interface{}
	argCount := 1

	if req.ValueText != nil {
		updates = append(updates, fmt.Sprintf("value_text = $%d", argCount))
		args = append(args, *req.ValueText)
		argCount++
	}

	if req.ValueNumber != nil {
		updates = append(updates, fmt.Sprintf("value_number = $%d", argCount))
		args = append(args, *req.ValueNumber)
		argCount++
	}

	if req.ValueBoolean != nil {
		updates = append(updates, fmt.Sprintf("value_boolean = $%d", argCount))
		args = append(args, *req.ValueBoolean)
		argCount++
	}

	if req.ValueDate != nil {
		updates = append(updates, fmt.Sprintf("value_date = $%d", argCount))
		args = append(args, *req.ValueDate)
		argCount++
	}

	if req.Metadata != nil {
		updates = append(updates, fmt.Sprintf("metadata = $%d", argCount))
		args = append(args, req.Metadata)
		argCount++
	}

	if req.Language != nil {
		updates = append(updates, fmt.Sprintf("language = $%d", argCount))
		args = append(args, *req.Language)
		argCount++
	}

	if req.UserIdentifier != nil {
		updates = append(updates, fmt.Sprintf("user_identifier = $%d", argCount))
		args = append(args, *req.UserIdentifier)
		argCount++
	}

	if len(updates) == 0 {
		return "", nil, false
	}

	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, updatedAt)
	argCount++

	args = append(args, id)

	query := fmt.Sprintf(`
		UPDATE feedback_records
		SET %s
		WHERE id = $%d
		RETURNING id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_identifier, tenant_id, response_id
	`, strings.Join(updates, ", "), argCount)

	return query, args, true
}

// Update updates an existing feedback record
// Only value fields, metadata, language, and user_identifier can be updated
func (r *FeedbackRecordsRepository) Update(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	query, args, hasUpdates := buildUpdateQuery(req, id, time.Now())
	if !hasUpdates {
		return r.GetByID(ctx, id)
	}

	var record models.FeedbackRecord
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.SourceType, &record.SourceID, &record.SourceName,
		&record.FieldID, &record.FieldLabel, &record.FieldType,
		&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
		&record.Metadata, &record.Language, &record.UserIdentifier, &record.TenantID, &record.ResponseID,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.NewNotFoundError("feedback record", "feedback record not found")
		}
		return nil, fmt.Errorf("failed to update feedback record: %w", err)
	}

	return &record, nil
}

// Delete removes a feedback record
func (r *FeedbackRecordsRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM feedback_records WHERE id = $1`

	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete feedback record: %w", err)
	}

	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("feedback record", "feedback record not found")
	}

	return nil
}

// BulkDelete deletes all feedback records matching user_identifier and optional tenant_id
func (r *FeedbackRecordsRepository) BulkDelete(ctx context.Context, userIdentifier string, tenantID *string) (int64, error) {
	query := `DELETE FROM feedback_records WHERE user_identifier = $1`
	args := []interface{}{userIdentifier}
	argCount := 2

	if tenantID != nil {
		query += fmt.Sprintf(" AND tenant_id = $%d", argCount)
		args = append(args, *tenantID)
	}

	result, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to bulk delete feedback records: %w", err)
	}

	return result.RowsAffected(), nil
}

// UpdateEnrichment updates the embedding for a feedback record
func (r *FeedbackRecordsRepository) UpdateEnrichment(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackEnrichmentRequest) error {
	query := `
		UPDATE feedback_records
		SET embedding = $1, updated_at = $2
		WHERE id = $3
	`

	var embeddingValue interface{}
	if req.Embedding != nil {
		embeddingValue = pgvector.NewVector(req.Embedding)
	}

	result, err := r.db.Exec(ctx, query, embeddingValue, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update feedback record enrichment: %w", err)
	}

	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("feedback record", "feedback record not found")
	}

	return nil
}

// ListBySimilarityWithDescendants finds feedback similar to a topic AND all its descendants.
// Uses a single optimized query with recursive CTE for efficiency.
// Returns the matching records and total count in one database round-trip.
func (r *FeedbackRecordsRepository) ListBySimilarityWithDescendants(
	ctx context.Context,
	topicID uuid.UUID,
	levelThresholds map[int]float64,
	defaultThreshold float64,
	filters *models.ListFeedbackRecordsFilters,
) ([]models.FeedbackRecord, int64, error) {
	// Extract threshold values for each level (1-5), using default for missing levels
	getThreshold := func(level int) float64 {
		if t, ok := levelThresholds[level]; ok {
			return t
		}
		return defaultThreshold
	}

	// Build additional filter conditions
	filterConditions, filterArgs, nextArg := buildSimilarityFilterConditions(filters, 10)

	// Build the optimized query with recursive CTE
	// This query:
	// 1. Gets target topic + all descendants via recursive CTE
	// 2. Computes similarity for each (topic, feedback) pair
	// 3. Applies level-appropriate threshold using CASE
	// 4. Keeps best match per feedback record using DISTINCT ON
	// 5. Returns total count via window function
	query := fmt.Sprintf(`
		WITH RECURSIVE topic_tree AS (
			-- Base: target topic
			SELECT id, level, embedding
			FROM topics
			WHERE id = $1 AND embedding IS NOT NULL
			
			UNION ALL
			
			-- Recursive: descendants with embeddings
			SELECT t.id, t.level, t.embedding
			FROM topics t
			INNER JOIN topic_tree tt ON t.parent_id = tt.id
			WHERE t.embedding IS NOT NULL
		),
		all_matches AS (
			-- Find feedback similar to ANY topic in tree
			SELECT 
				fr.id, fr.collected_at, fr.created_at, fr.updated_at,
				fr.source_type, fr.source_id, fr.source_name,
				fr.field_id, fr.field_label, fr.field_type,
				fr.value_text, fr.value_number, fr.value_boolean, fr.value_date,
				fr.metadata, fr.language, fr.user_identifier, fr.tenant_id, fr.response_id,
				1 - (fr.embedding <=> tt.embedding) as similarity
			FROM feedback_records fr
			CROSS JOIN topic_tree tt
			WHERE fr.embedding IS NOT NULL
			  AND 1 - (fr.embedding <=> tt.embedding) >= 
				  CASE tt.level
					  WHEN 1 THEN $2
					  WHEN 2 THEN $3
					  WHEN 3 THEN $4
					  WHEN 4 THEN $5
					  WHEN 5 THEN $6
					  ELSE $7
				  END
			  %s
		),
		deduplicated AS (
			-- Keep only the highest similarity per feedback record
			SELECT DISTINCT ON (id)
				id, collected_at, created_at, updated_at,
				source_type, source_id, source_name,
				field_id, field_label, field_type,
				value_text, value_number, value_boolean, value_date,
				metadata, language, user_identifier, tenant_id, response_id,
				similarity
			FROM all_matches
			ORDER BY id, similarity DESC
		)
		SELECT 
			id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_identifier, tenant_id, response_id,
			similarity,
			COUNT(*) OVER() as total_count
		FROM deduplicated
		ORDER BY similarity DESC
		LIMIT $8 OFFSET $9
	`, filterConditions)

	// Build args: topicID, thresholds (1-5), default, limit, offset, then filter args
	limit := filters.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := filters.Offset

	args := []interface{}{
		topicID,
		getThreshold(1),
		getThreshold(2),
		getThreshold(3),
		getThreshold(4),
		getThreshold(5),
		defaultThreshold,
		limit,
		offset,
	}
	args = append(args, filterArgs...)
	_ = nextArg // unused but returned by helper

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list feedback records by similarity with descendants: %w", err)
	}
	defer rows.Close()

	records := []models.FeedbackRecord{}
	var totalCount int64

	for rows.Next() {
		var record models.FeedbackRecord
		var similarity float64
		var count int64

		err := rows.Scan(
			&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
			&record.SourceType, &record.SourceID, &record.SourceName,
			&record.FieldID, &record.FieldLabel, &record.FieldType,
			&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
			&record.Metadata, &record.Language, &record.UserIdentifier, &record.TenantID, &record.ResponseID,
			&similarity, &count,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan feedback record: %w", err)
		}

		record.Similarity = &similarity
		records = append(records, record)
		totalCount = count // Same for all rows due to window function
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating feedback records: %w", err)
	}

	return records, totalCount, nil
}

// ListByTopicWithDescendants retrieves feedback records assigned to a topic or its descendants.
// Uses the pre-computed topic_id column set during taxonomy generation.
// This is faster than similarity search and provides accurate cluster-based results.
func (r *FeedbackRecordsRepository) ListByTopicWithDescendants(
	ctx context.Context,
	topicID uuid.UUID,
	filters *models.ListFeedbackRecordsFilters,
) ([]models.FeedbackRecord, int64, error) {
	// Build additional filter conditions
	filterConditions, filterArgs, _ := buildSimilarityFilterConditions(filters, 3)

	limit := filters.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := filters.Offset

	// Build query with recursive CTE to get topic and all descendants
	// Then match feedback records by topic_id column
	query := fmt.Sprintf(`
		WITH RECURSIVE topic_tree AS (
			SELECT id
			FROM topics
			WHERE id = $1
			
			UNION ALL
			
			SELECT t.id
			FROM topics t
			INNER JOIN topic_tree tt ON t.parent_id = tt.id
		)
		SELECT 
			fr.id, fr.collected_at, fr.created_at, fr.updated_at,
			fr.source_type, fr.source_id, fr.source_name,
			fr.field_id, fr.field_label, fr.field_type,
			fr.value_text, fr.value_number, fr.value_boolean, fr.value_date,
			fr.metadata, fr.language, fr.user_identifier, fr.tenant_id, fr.response_id,
			fr.classification_confidence,
			COUNT(*) OVER() as total_count
		FROM feedback_records fr
		WHERE fr.topic_id IN (SELECT id FROM topic_tree)
		%s
		ORDER BY fr.collected_at DESC
		LIMIT $2 OFFSET $3
	`, filterConditions)

	args := []interface{}{topicID, limit, offset}
	args = append(args, filterArgs...)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list feedback records by topic: %w", err)
	}
	defer rows.Close()

	records := []models.FeedbackRecord{}
	var totalCount int64

	for rows.Next() {
		var record models.FeedbackRecord
		var confidence *float64
		var count int64

		err := rows.Scan(
			&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
			&record.SourceType, &record.SourceID, &record.SourceName,
			&record.FieldID, &record.FieldLabel, &record.FieldType,
			&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
			&record.Metadata, &record.Language, &record.UserIdentifier, &record.TenantID, &record.ResponseID,
			&confidence, &count,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan feedback record: %w", err)
		}

		record.Similarity = confidence // Reuse similarity field for confidence
		records = append(records, record)
		totalCount = count
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating feedback records: %w", err)
	}

	return records, totalCount, nil
}

// buildSimilarityFilterConditions builds WHERE clause conditions for similarity queries.
// Returns the conditions string (with AND prefix for each), the args slice, and the next arg index.
// startArg is the first parameter index to use.
func buildSimilarityFilterConditions(filters *models.ListFeedbackRecordsFilters, startArg int) (string, []interface{}, int) {
	var conditions []string
	var args []interface{}
	argCount := startArg

	if filters.TenantID != nil {
		conditions = append(conditions, fmt.Sprintf("AND fr.tenant_id = $%d", argCount))
		args = append(args, *filters.TenantID)
		argCount++
	}
	if filters.ResponseID != nil {
		conditions = append(conditions, fmt.Sprintf("AND fr.response_id = $%d", argCount))
		args = append(args, *filters.ResponseID)
		argCount++
	}
	if filters.SourceType != nil {
		conditions = append(conditions, fmt.Sprintf("AND fr.source_type = $%d", argCount))
		args = append(args, *filters.SourceType)
		argCount++
	}
	if filters.SourceID != nil {
		conditions = append(conditions, fmt.Sprintf("AND fr.source_id = $%d", argCount))
		args = append(args, *filters.SourceID)
		argCount++
	}
	if filters.FieldID != nil {
		conditions = append(conditions, fmt.Sprintf("AND fr.field_id = $%d", argCount))
		args = append(args, *filters.FieldID)
		argCount++
	}
	if filters.FieldType != nil {
		conditions = append(conditions, fmt.Sprintf("AND fr.field_type = $%d", argCount))
		args = append(args, *filters.FieldType)
		argCount++
	}
	if filters.UserIdentifier != nil {
		conditions = append(conditions, fmt.Sprintf("AND fr.user_identifier = $%d", argCount))
		args = append(args, *filters.UserIdentifier)
		argCount++
	}
	if filters.Since != nil {
		conditions = append(conditions, fmt.Sprintf("AND fr.collected_at >= $%d", argCount))
		args = append(args, *filters.Since)
		argCount++
	}
	if filters.Until != nil {
		conditions = append(conditions, fmt.Sprintf("AND fr.collected_at <= $%d", argCount))
		args = append(args, *filters.Until)
		argCount++
	}

	return strings.Join(conditions, " "), args, argCount
}
