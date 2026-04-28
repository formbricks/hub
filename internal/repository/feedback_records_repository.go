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

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

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
			metadata, language, user_id, tenant_id, submission_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type, field_group_id, field_group_label,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_id, tenant_id, submission_id
	`

	var record models.FeedbackRecord

	err := r.db.QueryRow(ctx, query,
		collectedAt, req.SourceType, req.SourceID, req.SourceName,
		req.FieldID, req.FieldLabel, req.FieldType, req.FieldGroupID, req.FieldGroupLabel,
		req.ValueText, req.ValueNumber, req.ValueBoolean, req.ValueDate,
		req.Metadata, req.Language, req.UserID, req.TenantID, req.SubmissionID,
	).Scan(
		&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.SourceType, &record.SourceID, &record.SourceName,
		&record.FieldID, &record.FieldLabel, &record.FieldType, &record.FieldGroupID, &record.FieldGroupLabel,
		&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
		&record.Metadata, &record.Language, &record.UserID, &record.TenantID, &record.SubmissionID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, huberrors.NewConflictError("a feedback record with this tenant_id, submission_id, and field_id already exists")
		}

		return nil, fmt.Errorf("failed to create feedback record: %w", err)
	}

	return &record, nil
}

// GetByID retrieves a single feedback record by ID. Embedding is not selected (API/worker reads stay lean).
func (r *FeedbackRecordsRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error) {
	query := `
		SELECT id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type, field_group_id, field_group_label,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_id, tenant_id, submission_id
		FROM feedback_records
		WHERE id = $1
	`

	var record models.FeedbackRecord

	err := r.db.QueryRow(ctx, query, id).Scan(
		&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.SourceType, &record.SourceID, &record.SourceName,
		&record.FieldID, &record.FieldLabel, &record.FieldType, &record.FieldGroupID, &record.FieldGroupLabel,
		&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
		&record.Metadata, &record.Language, &record.UserID, &record.TenantID, &record.SubmissionID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("feedback record", "feedback record not found")
		}

		return nil, fmt.Errorf("failed to get feedback record: %w", err)
	}

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

	if filters.SubmissionID != nil {
		conditions = append(conditions, fmt.Sprintf("submission_id = $%d", argCount))
		args = append(args, *filters.SubmissionID)
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

	if filters.UserID != nil {
		conditions = append(conditions, fmt.Sprintf("user_id = $%d", argCount))
		args = append(args, *filters.UserID)
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

const feedbackRecordsListSelect = `
		SELECT id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type, field_group_id, field_group_label,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_id, tenant_id, submission_id
		FROM feedback_records
	`

// List retrieves feedback records with optional filters. Embedding is not selected (API reads stay lean).
// Fetches limit+1 as sentinel to determine hasMore; returns trimmed slice and hasMore.
func (r *FeedbackRecordsRepository) List(
	ctx context.Context, filters *models.ListFeedbackRecordsFilters,
) ([]models.FeedbackRecord, bool, error) {
	query := feedbackRecordsListSelect

	whereClause, args := buildFilterConditions(filters)
	query += whereClause
	argCount := len(args) + 1

	query += " ORDER BY collected_at DESC, id ASC"

	limit := filters.Limit
	if limit <= 0 {
		limit = 100
	}

	query += fmt.Sprintf(" LIMIT $%d", argCount)

	args = append(args, limit+1)

	records, err := r.fetchFeedbackRecords(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}

	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}

	return records, hasMore, nil
}

// ListAfterCursor retrieves feedback records after the given keyset cursor (collected_at, id).
// Order is collected_at DESC, id ASC. The cursor represents the last row of the previous page.
// Fetches limit+1 as sentinel to determine hasMore; returns trimmed slice and hasMore.
func (r *FeedbackRecordsRepository) ListAfterCursor(
	ctx context.Context, filters *models.ListFeedbackRecordsFilters, cursorCollectedAt time.Time, cursorID uuid.UUID,
) ([]models.FeedbackRecord, bool, error) {
	query := feedbackRecordsListSelect

	whereClause, args := buildFilterConditions(filters)
	query += whereClause

	// Keyset condition: next page = (collected_at < cursor) OR (collected_at = cursor AND id > cursorID)
	// For ORDER BY collected_at DESC, id ASC (two cursor params: collected_at, id)
	argTime := len(args) + 1

	argID := len(args) + 2 //nolint:mnd // second keyset param
	if whereClause != "" {
		query += fmt.Sprintf(" AND (collected_at < $%d OR (collected_at = $%d AND id > $%d))", argTime, argTime, argID)
	} else {
		query += fmt.Sprintf(" WHERE (collected_at < $%d OR (collected_at = $%d AND id > $%d))", argTime, argTime, argID)
	}

	args = append(args, cursorCollectedAt, cursorID)
	argCount := len(args) + 1

	query += " ORDER BY collected_at DESC, id ASC"

	limit := filters.Limit
	if limit <= 0 {
		limit = 100
	}

	query += fmt.Sprintf(" LIMIT $%d", argCount)

	args = append(args, limit+1)

	records, err := r.fetchFeedbackRecords(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}

	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}

	return records, hasMore, nil
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

	if req.UserID != nil {
		updates = append(updates, fmt.Sprintf("user_id = $%d", argCount))
		args = append(args, *req.UserID)
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
			metadata, language, user_id, tenant_id, submission_id
	`, strings.Join(updates, ", "), argCount)

	return query, args, true
}

// Update updates an existing feedback record
// Only value fields, metadata, language, and user_id can be updated.
func (r *FeedbackRecordsRepository) Update(
	ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	query, args, hasUpdates := buildUpdateQuery(req, id, time.Now())
	if !hasUpdates {
		return r.GetByID(ctx, id)
	}

	var record models.FeedbackRecord

	err := r.db.QueryRow(ctx, query, args...).Scan(
		&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.SourceType, &record.SourceID, &record.SourceName,
		&record.FieldID, &record.FieldLabel, &record.FieldType, &record.FieldGroupID, &record.FieldGroupLabel,
		&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
		&record.Metadata, &record.Language, &record.UserID, &record.TenantID, &record.SubmissionID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("feedback record", "feedback record not found")
		}

		return nil, fmt.Errorf("failed to update feedback record: %w", err)
	}

	return &record, nil
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

// BulkDelete deletes all feedback records matching user_id.
// When tenant_id is provided, deletion is restricted to that tenant; otherwise all user records are deleted.
// It returns deleted IDs grouped by tenant so callers can publish tenant-scoped side effects.
func (r *FeedbackRecordsRepository) BulkDelete(
	ctx context.Context, filters *models.BulkDeleteFilters,
) ([]models.DeletedFeedbackRecordsByTenant, error) {
	query := `
		DELETE FROM feedback_records
		WHERE user_id = $1`
	args := []any{filters.UserID}

	if filters.TenantID != nil {
		query += ` AND tenant_id = $2`

		args = append(args, *filters.TenantID)
	}

	query += `
		RETURNING id, tenant_id`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk delete feedback records: %w", err)
	}
	defer rows.Close()

	groups := make([]models.DeletedFeedbackRecordsByTenant, 0)
	groupIndexByTenant := make(map[string]int)

	for rows.Next() {
		var (
			id       uuid.UUID
			tenantID string
		)

		if err := rows.Scan(&id, &tenantID); err != nil {
			return nil, fmt.Errorf("failed to scan deleted feedback record id: %w", err)
		}

		groupIndex, ok := groupIndexByTenant[tenantID]
		if !ok {
			groupIndex = len(groups)
			groupIndexByTenant[tenantID] = groupIndex
			groups = append(groups, models.DeletedFeedbackRecordsByTenant{TenantID: tenantID})
		}

		groups[groupIndex].IDs = append(groups[groupIndex].IDs, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating bulk delete result: %w", err)
	}

	return groups, nil
}

// fetchFeedbackRecords executes the given query and scans rows into FeedbackRecord slices.
// Used by List and ListAfterCursor to avoid duplicating SELECT/scan logic.
func (r *FeedbackRecordsRepository) fetchFeedbackRecords(
	ctx context.Context, query string, args ...any,
) ([]models.FeedbackRecord, error) {
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list feedback records: %w", err)
	}
	defer rows.Close()

	records := []models.FeedbackRecord{}

	for rows.Next() {
		var record models.FeedbackRecord

		err := rows.Scan(
			&record.ID, &record.CollectedAt, &record.CreatedAt, &record.UpdatedAt,
			&record.SourceType, &record.SourceID, &record.SourceName,
			&record.FieldID, &record.FieldLabel, &record.FieldType, &record.FieldGroupID, &record.FieldGroupLabel,
			&record.ValueText, &record.ValueNumber, &record.ValueBoolean, &record.ValueDate,
			&record.Metadata, &record.Language, &record.UserID, &record.TenantID, &record.SubmissionID,
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
