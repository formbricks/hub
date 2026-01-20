package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
			value_text, value_number, value_boolean, value_date, value_json,
			metadata, language, user_identifier, tenant_id, response_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date, value_json,
			metadata, language, user_identifier, tenant_id, response_id
	`

	var record models.FeedbackRecord
	err := r.db.QueryRow(ctx, query,
		collectedAt, req.SourceType, req.SourceID, req.SourceName,
		req.FieldID, req.FieldLabel, req.FieldType,
		req.ValueText, req.ValueNumber, req.ValueBoolean, req.ValueDate, req.ValueJSON,
		req.Metadata, req.Language, req.UserIdentifier, req.TenantID, req.ResponseID,
	).Scan(
		&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.SourceType, &record.SourceID, &record.SourceName,
		&record.FieldID, &record.FieldLabel, &record.FieldType,
		&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate, &record.ValueJSON,
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
			value_text, value_number, value_boolean, value_date, value_json,
			metadata, language, user_identifier, tenant_id, response_id
		FROM feedback_records
		WHERE id = $1
	`

	var record models.FeedbackRecord
	err := r.db.QueryRow(ctx, query, id).Scan(
		&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.SourceType, &record.SourceID, &record.SourceName,
		&record.FieldID, &record.FieldLabel, &record.FieldType,
		&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate, &record.ValueJSON,
		&record.Metadata, &record.Language, &record.UserIdentifier, &record.TenantID, &record.ResponseID,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("feedback record not found")
		}
		return nil, fmt.Errorf("failed to get feedback record: %w", err)
	}

	return &record, nil
}

// List retrieves feedback records with optional filters
func (r *FeedbackRecordsRepository) List(ctx context.Context, filters *models.ListFeedbackRecordsFilters) ([]models.FeedbackRecord, error) {
	query := `
		SELECT id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date, value_json,
			metadata, language, user_identifier, tenant_id, response_id
		FROM feedback_records
	`

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

	if filters.Since != nil {
		conditions = append(conditions, fmt.Sprintf("collected_at >= $%d", argCount))
		args = append(args, *filters.Since)
		argCount++
	}

	if filters.Until != nil {
		conditions = append(conditions, fmt.Sprintf("collected_at <= $%d", argCount))
		args = append(args, *filters.Until)
		argCount++
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	query += " ORDER BY collected_at DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, filters.Limit)
		argCount++
	}

	if filters.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argCount)
		args = append(args, filters.Offset)
		argCount++
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list feedback records: %w", err)
	}
	defer rows.Close()

	var records []models.FeedbackRecord
	for rows.Next() {
		var record models.FeedbackRecord
		err := rows.Scan(
			&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
			&record.SourceType, &record.SourceID, &record.SourceName,
			&record.FieldID, &record.FieldLabel, &record.FieldType,
			&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate, &record.ValueJSON,
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

	if filters.Since != nil {
		conditions = append(conditions, fmt.Sprintf("collected_at >= $%d", argCount))
		args = append(args, *filters.Since)
		argCount++
	}

	if filters.Until != nil {
		conditions = append(conditions, fmt.Sprintf("collected_at <= $%d", argCount))
		args = append(args, *filters.Until)
		argCount++
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	var count int64
	err := r.db.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count feedback records: %w", err)
	}

	return count, nil
}

// Update updates an existing feedback record
// Only value fields, metadata, language, and user_identifier can be updated
func (r *FeedbackRecordsRepository) Update(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
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

	if req.ValueJSON != nil {
		updates = append(updates, fmt.Sprintf("value_json = $%d", argCount))
		args = append(args, req.ValueJSON)
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
		return r.GetByID(ctx, id)
	}

	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())
	argCount++

	args = append(args, id)

	query := fmt.Sprintf(`
		UPDATE feedback_records
		SET %s
		WHERE id = $%d
		RETURNING id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date, value_json,
			metadata, language, user_identifier, tenant_id, response_id
	`, strings.Join(updates, ", "), argCount)

	var record models.FeedbackRecord
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.SourceType, &record.SourceID, &record.SourceName,
		&record.FieldID, &record.FieldLabel, &record.FieldType,
		&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate, &record.ValueJSON,
		&record.Metadata, &record.Language, &record.UserIdentifier, &record.TenantID, &record.ResponseID,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("feedback record not found")
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
		return fmt.Errorf("feedback record not found")
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
		argCount++
	}

	result, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to bulk delete feedback records: %w", err)
	}

	return result.RowsAffected(), nil
}

// Search performs semantic search with filters
// Note: Currently implements full-text search. Semantic search with embeddings will be added later.
func (r *FeedbackRecordsRepository) Search(ctx context.Context, req *models.SearchFeedbackRecordsRequest) ([]models.FeedbackRecord, error) {
	// Build base query
	baseQuery := `
		SELECT id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date, value_json,
			metadata, language, user_identifier, tenant_id, response_id
		FROM feedback_records
	`

	var conditions []string
	var args []interface{}
	argCount := 1

	// Full-text search on text fields (required for search endpoint)
	if req.Query == nil || *req.Query == "" {
		return nil, fmt.Errorf("query parameter is required")
	}

	// Search across multiple fields using the same search pattern
	// All four ILIKE conditions use $1 with the same value
	searchPattern := "%" + *req.Query + "%"
	conditions = append(conditions, `(
		value_text ILIKE $1 OR
		field_label ILIKE $1 OR
		source_name ILIKE $1 OR
		field_id ILIKE $1
	)`)
	args = append(args, searchPattern)
	argCount++

	// Filter by source_type
	if req.SourceType != nil {
		conditions = append(conditions, fmt.Sprintf("source_type = $%d", argCount))
		args = append(args, *req.SourceType)
		argCount++
	}

	// Filter by date range (since/until)
	if req.Since != nil {
		conditions = append(conditions, fmt.Sprintf("collected_at >= $%d", argCount))
		args = append(args, *req.Since)
		argCount++
	}

	if req.Until != nil {
		conditions = append(conditions, fmt.Sprintf("collected_at <= $%d", argCount))
		args = append(args, *req.Until)
		argCount++
	}

	// Add WHERE clause
	whereClause := " WHERE " + strings.Join(conditions, " AND ")

	// Add ORDER BY
	orderBy := " ORDER BY collected_at DESC"

	// Set default limit if not provided
	limit := req.Limit
	if limit <= 0 {
		limit = 10 // Default limit
	}
	if limit > 100 {
		limit = 100 // Max limit
	}

	// Add pagination
	paginationClause := fmt.Sprintf(" LIMIT $%d", argCount)
	args = append(args, limit)

	// Execute search query
	fullQuery := baseQuery + whereClause + orderBy + paginationClause
	rows, err := r.db.Query(ctx, fullQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search feedback records: %w", err)
	}
	defer rows.Close()

	var records []models.FeedbackRecord
	for rows.Next() {
		var record models.FeedbackRecord
		err := rows.Scan(
			&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
			&record.SourceType, &record.SourceID, &record.SourceName,
			&record.FieldID, &record.FieldLabel, &record.FieldType,
			&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate, &record.ValueJSON,
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
