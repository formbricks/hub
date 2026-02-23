// Package repository provides data access for feedback records.
package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// errEmbeddingScanInvalidType is returned when Scan receives a type other than []byte.
var errEmbeddingScanInvalidType = errors.New("embedding: expected []byte")

// nullableEmbedding scans a vector column that may be NULL without panicking (pgvector.Vector.Scan panics on empty/NULL).
type nullableEmbedding []float32

func (n *nullableEmbedding) Scan(src any) error {
	if src == nil {
		*n = nil

		return nil
	}

	buf, ok := src.([]byte)
	if !ok {
		return fmt.Errorf("%w: got %T", errEmbeddingScanInvalidType, src)
	}

	if len(buf) == 0 {
		*n = nil

		return nil
	}

	var vec pgvector.Vector

	if err := vec.DecodeBinary(buf); err != nil {
		return fmt.Errorf("embedding decode: %w", err)
	}

	*n = vec.Slice()

	return nil
}

// FeedbackRecordsRepository handles data access for feedback records.
type FeedbackRecordsRepository struct {
	db *pgxpool.Pool
}

// NewFeedbackRecordsRepository creates a new feedback records repository.
func NewFeedbackRecordsRepository(db *pgxpool.Pool) *FeedbackRecordsRepository {
	return &FeedbackRecordsRepository{db: db}
}

// Create inserts a new feedback record.
func (r *FeedbackRecordsRepository) Create(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	collectedAt := time.Now()
	if req.CollectedAt != nil {
		collectedAt = *req.CollectedAt
	}

	query := `
		INSERT INTO feedback_records (
			collected_at, source_type, source_id, source_name,
			field_id, field_label, field_type, field_group_id, field_group_label,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_identifier, tenant_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type, field_group_id, field_group_label,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_identifier, tenant_id
	`

	var record models.FeedbackRecord

	err := r.db.QueryRow(ctx, query,
		collectedAt, req.SourceType, req.SourceID, req.SourceName,
		req.FieldID, req.FieldLabel, req.FieldType, req.FieldGroupID, req.FieldGroupLabel,
		req.ValueText, req.ValueNumber, req.ValueBoolean, req.ValueDate,
		req.Metadata, req.Language, req.UserIdentifier, req.TenantID,
	).Scan(
		&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.SourceType, &record.SourceID, &record.SourceName,
		&record.FieldID, &record.FieldLabel, &record.FieldType, &record.FieldGroupID, &record.FieldGroupLabel,
		&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
		&record.Metadata, &record.Language, &record.UserIdentifier, &record.TenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create feedback record: %w", err)
	}

	return &record, nil
}

// GetByID retrieves a single feedback record by ID.
func (r *FeedbackRecordsRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error) {
	query := `
		SELECT id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type, field_group_id, field_group_label,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_identifier, tenant_id, embedding
		FROM feedback_records
		WHERE id = $1
	`

	var record models.FeedbackRecord

	var emb nullableEmbedding

	err := r.db.QueryRow(ctx, query, id).Scan(
		&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.SourceType, &record.SourceID, &record.SourceName,
		&record.FieldID, &record.FieldLabel, &record.FieldType, &record.FieldGroupID, &record.FieldGroupLabel,
		&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
		&record.Metadata, &record.Language, &record.UserIdentifier, &record.TenantID,
		&emb,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("feedback record", "feedback record not found")
		}

		return nil, fmt.Errorf("failed to get feedback record: %w", err)
	}

	record.Embedding = emb

	return &record, nil
}

// buildFilterConditions builds WHERE clause conditions and arguments from filters.
// Returns the WHERE clause (including " WHERE " prefix if conditions exist) and the args slice.
func buildFilterConditions(filters *models.ListFeedbackRecordsFilters) (whereClause string, args []any) {
	var conditions []string

	argCount := 1

	if filters.TenantID != nil {
		conditions = append(conditions, fmt.Sprintf("tenant_id = $%d", argCount))
		args = append(args, *filters.TenantID)
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

	if filters.FieldGroupID != nil {
		conditions = append(conditions, fmt.Sprintf("field_group_id = $%d", argCount))
		args = append(args, *filters.FieldGroupID)
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

	if filters.Since != nil {
		conditions = append(conditions, fmt.Sprintf("collected_at >= $%d", argCount))
		args = append(args, *filters.Since)
		argCount++
	}

	if filters.Until != nil {
		conditions = append(conditions, fmt.Sprintf("collected_at <= $%d", argCount))
		args = append(args, *filters.Until)
	}

	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	return whereClause, args
}

// List retrieves feedback records with optional filters.
func (r *FeedbackRecordsRepository) List(ctx context.Context, filters *models.ListFeedbackRecordsFilters) ([]models.FeedbackRecord, error) {
	query := `
		SELECT id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type, field_group_id, field_group_label,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_identifier, tenant_id, embedding
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

		var emb nullableEmbedding

		err := rows.Scan(
			&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
			&record.SourceType, &record.SourceID, &record.SourceName,
			&record.FieldID, &record.FieldLabel, &record.FieldType, &record.FieldGroupID, &record.FieldGroupLabel,
			&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
			&record.Metadata, &record.Language, &record.UserIdentifier, &record.TenantID,
			&emb,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan feedback record: %w", err)
		}

		record.Embedding = emb

		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating feedback records: %w", err)
	}

	return records, nil
}

// Count returns the total count of feedback records matching the filters.
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

// buildUpdateQuery builds an UPDATE query with SET clause and arguments.
// Returns the query string, arguments, and a boolean indicating if any updates were provided.
func buildUpdateQuery(
	req *models.UpdateFeedbackRecordRequest, id uuid.UUID, updatedAt time.Time,
) (query string, args []any, hasUpdates bool) {
	var updates []string

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

	query = fmt.Sprintf(`
		UPDATE feedback_records
		SET %s
		WHERE id = $%d
		RETURNING id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type, field_group_id, field_group_label,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_identifier, tenant_id, embedding
	`, strings.Join(updates, ", "), argCount)

	return query, args, true
}

// Update updates an existing feedback record
// Only value fields, metadata, language, and user_identifier can be updated.
func (r *FeedbackRecordsRepository) Update(
	ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	query, args, hasUpdates := buildUpdateQuery(req, id, time.Now())
	if !hasUpdates {
		return r.GetByID(ctx, id)
	}

	var record models.FeedbackRecord

	var emb nullableEmbedding

	err := r.db.QueryRow(ctx, query, args...).Scan(
		&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.SourceType, &record.SourceID, &record.SourceName,
		&record.FieldID, &record.FieldLabel, &record.FieldType, &record.FieldGroupID, &record.FieldGroupLabel,
		&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
		&record.Metadata, &record.Language, &record.UserIdentifier, &record.TenantID,
		&emb,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("feedback record", "feedback record not found")
		}

		return nil, fmt.Errorf("failed to update feedback record: %w", err)
	}

	record.Embedding = emb

	return &record, nil
}

// UpdateEmbedding sets the embedding vector for a feedback record. Pass nil to clear the embedding (set to NULL).
func (r *FeedbackRecordsRepository) UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error {
	var result pgconn.CommandTag

	var err error

	if embedding == nil {
		result, err = r.db.Exec(ctx,
			`UPDATE feedback_records SET embedding = NULL, updated_at = $1 WHERE id = $2`,
			time.Now(), id,
		)
	} else {
		vec := pgvector.NewVector(embedding)

		result, err = r.db.Exec(ctx,
			`UPDATE feedback_records SET embedding = $1, updated_at = $2 WHERE id = $3`,
			vec, time.Now(), id,
		)
	}

	if err != nil {
		return fmt.Errorf("failed to update feedback record embedding: %w", err)
	}

	if result.RowsAffected() == 0 {
		return huberrors.NewNotFoundError("feedback record", "feedback record not found")
	}

	return nil
}

// ListIDsForEmbeddingBackfill returns IDs of feedback records that have non-empty value_text and null embedding.
func (r *FeedbackRecordsRepository) ListIDsForEmbeddingBackfill(ctx context.Context) ([]uuid.UUID, error) {
	query := `
		SELECT id FROM feedback_records
		WHERE embedding IS NULL
		  AND value_text IS NOT NULL
		  AND trim(value_text) != ''
	`

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list ids for embedding backfill: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID

	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan feedback record id: %w", err)
		}

		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating embedding backfill ids: %w", err)
	}

	return ids, nil
}

// Delete removes a feedback record.
func (r *FeedbackRecordsRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM feedback_records WHERE id = $1`

	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete feedback record: %w", err)
	}

	if result.RowsAffected() == 0 {
		return huberrors.NewNotFoundError("feedback record", "feedback record not found")
	}

	return nil
}

// BulkDelete deletes all feedback records matching user_identifier and optional tenant_id.
// It returns the deleted IDs (via RETURNING id) so callers can e.g. publish events.
func (r *FeedbackRecordsRepository) BulkDelete(ctx context.Context, userIdentifier string, tenantID *string) ([]uuid.UUID, error) {
	query := `
		DELETE FROM feedback_records
		WHERE user_identifier = $1`
	args := []any{userIdentifier}
	argCount := 2

	if tenantID != nil {
		query += fmt.Sprintf(" AND tenant_id = $%d", argCount)

		args = append(args, *tenantID)
	}

	query += ` RETURNING id`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk delete feedback records: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID

	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan deleted feedback record id: %w", err)
		}

		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating bulk delete result: %w", err)
	}

	return ids, nil
}
