package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xernobyl/formbricks_worktrial/internal/models"
)

// ExperienceRepository handles data access for experience data
type ExperienceRepository struct {
	db *pgxpool.Pool
}

// NewExperienceRepository creates a new experience repository
func NewExperienceRepository(db *pgxpool.Pool) *ExperienceRepository {
	return &ExperienceRepository{db: db}
}

// Create inserts a new experience data record
func (r *ExperienceRepository) Create(ctx context.Context, req *models.CreateExperienceRequest) (*models.ExperienceData, error) {
	collectedAt := time.Now()
	if req.CollectedAt != nil {
		collectedAt = *req.CollectedAt
	}

	query := `
		INSERT INTO experience_data (
			collected_at, source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date, value_json,
			metadata, language, user_identifier
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date, value_json,
			metadata, language, user_identifier
	`

	var exp models.ExperienceData
	err := r.db.QueryRow(ctx, query,
		collectedAt, req.SourceType, req.SourceID, req.SourceName,
		req.FieldID, req.FieldLabel, req.FieldType,
		req.ValueText, req.ValueNumber, req.ValueBoolean, req.ValueDate, req.ValueJSON,
		req.Metadata, req.Language, req.UserIdentifier,
	).Scan(
		&exp.ID, &exp.CollectedAt, &exp.CreatedAt, &exp.UpdatedAt,
		&exp.SourceType, &exp.SourceID, &exp.SourceName,
		&exp.FieldID, &exp.FieldLabel, &exp.FieldType,
		&exp.ValueText, &exp.ValueNumber, &exp.ValueBoolean, &exp.ValueDate, &exp.ValueJSON,
		&exp.Metadata, &exp.Language, &exp.UserIdentifier,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create experience: %w", err)
	}

	return &exp, nil
}

// GetByID retrieves a single experience data record by ID
func (r *ExperienceRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.ExperienceData, error) {
	query := `
		SELECT id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date, value_json,
			metadata, language, user_identifier
		FROM experience_data
		WHERE id = $1
	`

	var exp models.ExperienceData
	err := r.db.QueryRow(ctx, query, id).Scan(
		&exp.ID, &exp.CollectedAt, &exp.CreatedAt, &exp.UpdatedAt,
		&exp.SourceType, &exp.SourceID, &exp.SourceName,
		&exp.FieldID, &exp.FieldLabel, &exp.FieldType,
		&exp.ValueText, &exp.ValueNumber, &exp.ValueBoolean, &exp.ValueDate, &exp.ValueJSON,
		&exp.Metadata, &exp.Language, &exp.UserIdentifier,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("experience not found")
		}
		return nil, fmt.Errorf("failed to get experience: %w", err)
	}

	return &exp, nil
}

// List retrieves experience data records with optional filters
func (r *ExperienceRepository) List(ctx context.Context, filters *models.ListExperiencesFilters) ([]models.ExperienceData, error) {
	query := `
		SELECT id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date, value_json,
			metadata, language, user_identifier
		FROM experience_data
	`

	var conditions []string
	var args []interface{}
	argCount := 1

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

	if filters.UserIdentifier != nil {
		conditions = append(conditions, fmt.Sprintf("user_identifier = $%d", argCount))
		args = append(args, *filters.UserIdentifier)
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
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list experiences: %w", err)
	}
	defer rows.Close()

	var experiences []models.ExperienceData
	for rows.Next() {
		var exp models.ExperienceData
		err := rows.Scan(
			&exp.ID, &exp.CollectedAt, &exp.CreatedAt, &exp.UpdatedAt,
			&exp.SourceType, &exp.SourceID, &exp.SourceName,
			&exp.FieldID, &exp.FieldLabel, &exp.FieldType,
			&exp.ValueText, &exp.ValueNumber, &exp.ValueBoolean, &exp.ValueDate, &exp.ValueJSON,
			&exp.Metadata, &exp.Language, &exp.UserIdentifier,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan experience: %w", err)
		}
		experiences = append(experiences, exp)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating experiences: %w", err)
	}

	return experiences, nil
}

// Update updates an existing experience data record
func (r *ExperienceRepository) Update(ctx context.Context, id uuid.UUID, req *models.UpdateExperienceRequest) (*models.ExperienceData, error) {
	var updates []string
	var args []interface{}
	argCount := 1

	if req.SourceType != nil {
		updates = append(updates, fmt.Sprintf("source_type = $%d", argCount))
		args = append(args, *req.SourceType)
		argCount++
	}

	if req.SourceID != nil {
		updates = append(updates, fmt.Sprintf("source_id = $%d", argCount))
		args = append(args, *req.SourceID)
		argCount++
	}

	if req.SourceName != nil {
		updates = append(updates, fmt.Sprintf("source_name = $%d", argCount))
		args = append(args, *req.SourceName)
		argCount++
	}

	if req.FieldID != nil {
		updates = append(updates, fmt.Sprintf("field_id = $%d", argCount))
		args = append(args, *req.FieldID)
		argCount++
	}

	if req.FieldLabel != nil {
		updates = append(updates, fmt.Sprintf("field_label = $%d", argCount))
		args = append(args, *req.FieldLabel)
		argCount++
	}

	if req.FieldType != nil {
		updates = append(updates, fmt.Sprintf("field_type = $%d", argCount))
		args = append(args, *req.FieldType)
		argCount++
	}

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
		UPDATE experience_data
		SET %s
		WHERE id = $%d
		RETURNING id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date, value_json,
			metadata, language, user_identifier
	`, strings.Join(updates, ", "), argCount)

	var exp models.ExperienceData
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&exp.ID, &exp.CollectedAt, &exp.CreatedAt, &exp.UpdatedAt,
		&exp.SourceType, &exp.SourceID, &exp.SourceName,
		&exp.FieldID, &exp.FieldLabel, &exp.FieldType,
		&exp.ValueText, &exp.ValueNumber, &exp.ValueBoolean, &exp.ValueDate, &exp.ValueJSON,
		&exp.Metadata, &exp.Language, &exp.UserIdentifier,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("experience not found")
		}
		return nil, fmt.Errorf("failed to update experience: %w", err)
	}

	return &exp, nil
}

// Delete removes an experience data record
func (r *ExperienceRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM experience_data WHERE id = $1`

	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete experience: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("experience not found")
	}

	return nil
}

// Search performs advanced search with filters and pagination
func (r *ExperienceRepository) Search(ctx context.Context, req *models.SearchExperiencesRequest) ([]models.ExperienceData, int, error) {
	// Build base query
	baseQuery := `
		SELECT id, collected_at, created_at, updated_at,
			source_type, source_id, source_name,
			field_id, field_label, field_type,
			value_text, value_number, value_boolean, value_date, value_json,
			metadata, language, user_identifier
		FROM experience_data
	`

	countQuery := `SELECT COUNT(*) FROM experience_data`

	var conditions []string
	var args []interface{}
	argCount := 1

	// Full-text search on text fields
	if req.Query != nil && *req.Query != "" {
		conditions = append(conditions, fmt.Sprintf(`(
			value_text ILIKE $%d OR
			field_label ILIKE $%d OR
			source_name ILIKE $%d OR
			field_id ILIKE $%d
		)`, argCount, argCount, argCount, argCount))
		args = append(args, "%"+*req.Query+"%")
		argCount++
	}

	// Filter by source_type
	if req.SourceType != nil {
		conditions = append(conditions, fmt.Sprintf("source_type = $%d", argCount))
		args = append(args, *req.SourceType)
		argCount++
	}

	// Filter by source_id
	if req.SourceID != nil {
		conditions = append(conditions, fmt.Sprintf("source_id = $%d", argCount))
		args = append(args, *req.SourceID)
		argCount++
	}

	// Filter by field_id
	if req.FieldID != nil {
		conditions = append(conditions, fmt.Sprintf("field_id = $%d", argCount))
		args = append(args, *req.FieldID)
		argCount++
	}

	// Filter by field_type
	if req.FieldType != nil {
		conditions = append(conditions, fmt.Sprintf("field_type = $%d", argCount))
		args = append(args, *req.FieldType)
		argCount++
	}

	// Filter by user_identifier
	if req.UserIdentifier != nil {
		conditions = append(conditions, fmt.Sprintf("user_identifier = $%d", argCount))
		args = append(args, *req.UserIdentifier)
		argCount++
	}

	// Filter by date range
	if req.StartDate != nil {
		conditions = append(conditions, fmt.Sprintf("collected_at >= $%d", argCount))
		args = append(args, *req.StartDate)
		argCount++
	}

	if req.EndDate != nil {
		conditions = append(conditions, fmt.Sprintf("collected_at <= $%d", argCount))
		args = append(args, *req.EndDate)
		argCount++
	}

	// Add WHERE clause if conditions exist
	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	// Get total count
	var totalCount int
	err := r.db.QueryRow(ctx, countQuery+whereClause, args...).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count experiences: %w", err)
	}

	// Add ORDER BY
	orderBy := " ORDER BY collected_at DESC"

	// Calculate limit and offset based on page and pageSize
	limit := req.PageSize
	offset := req.Page * req.PageSize

	// Add pagination
	paginationClause := fmt.Sprintf(" LIMIT $%d OFFSET $%d", argCount, argCount+1)
	args = append(args, limit, offset)

	// Execute search query
	fullQuery := baseQuery + whereClause + orderBy + paginationClause
	rows, err := r.db.Query(ctx, fullQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search experiences: %w", err)
	}
	defer rows.Close()

	var experiences []models.ExperienceData
	for rows.Next() {
		var exp models.ExperienceData
		err := rows.Scan(
			&exp.ID, &exp.CollectedAt, &exp.CreatedAt, &exp.UpdatedAt,
			&exp.SourceType, &exp.SourceID, &exp.SourceName,
			&exp.FieldID, &exp.FieldLabel, &exp.FieldType,
			&exp.ValueText, &exp.ValueNumber, &exp.ValueBoolean, &exp.ValueDate, &exp.ValueJSON,
			&exp.Metadata, &exp.Language, &exp.UserIdentifier,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan experience: %w", err)
		}
		experiences = append(experiences, exp)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating experiences: %w", err)
	}

	return experiences, totalCount, nil
}
