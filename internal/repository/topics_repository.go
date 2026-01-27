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

// TopicsRepository handles data access for topics
type TopicsRepository struct {
	db *pgxpool.Pool
}

// NewTopicsRepository creates a new topics repository
func NewTopicsRepository(db *pgxpool.Pool) *TopicsRepository {
	return &TopicsRepository{db: db}
}

// Create inserts a new topic with the specified level
func (r *TopicsRepository) Create(ctx context.Context, req *models.CreateTopicRequest) (*models.Topic, error) {
	tenantID := normalizeTenantID(req.TenantID)

	query := `
		INSERT INTO topics (title, level, parent_id, tenant_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, title, level, parent_id, tenant_id, created_at, updated_at
	`

	var topic models.Topic
	err := r.db.QueryRow(ctx, query, req.Title, req.Level, req.ParentID, tenantID).Scan(
		&topic.ID, &topic.Title, &topic.Level, &topic.ParentID, &topic.TenantID, &topic.CreatedAt, &topic.UpdatedAt,
	)
	if err != nil {
		// Check for unique constraint violation
		if strings.Contains(err.Error(), "duplicate key value violates unique constraint") ||
			strings.Contains(err.Error(), "23505") {
			return nil, apperrors.NewConflictError("topic", "topic with this title already exists")
		}
		return nil, fmt.Errorf("failed to create topic: %w", err)
	}

	return &topic, nil
}

// GetByID retrieves a single topic by ID
func (r *TopicsRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.Topic, error) {
	query := `
		SELECT id, title, level, parent_id, tenant_id, created_at, updated_at
		FROM topics
		WHERE id = $1
	`

	var topic models.Topic
	err := r.db.QueryRow(ctx, query, id).Scan(
		&topic.ID, &topic.Title, &topic.Level, &topic.ParentID, &topic.TenantID, &topic.CreatedAt, &topic.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.NewNotFoundError("topic", "topic not found")
		}
		return nil, fmt.Errorf("failed to get topic: %w", err)
	}

	return &topic, nil
}

// buildTopicsFilterConditions builds WHERE clause conditions and arguments from filters
func buildTopicsFilterConditions(filters *models.ListTopicsFilters) (string, []interface{}) {
	var conditions []string
	var args []interface{}
	argCount := 1

	if filters.Level != nil {
		conditions = append(conditions, fmt.Sprintf("level = $%d", argCount))
		args = append(args, *filters.Level)
		argCount++
	}

	if filters.ParentID != nil {
		conditions = append(conditions, fmt.Sprintf("parent_id = $%d", argCount))
		args = append(args, *filters.ParentID)
		argCount++
	}

	if filters.Title != nil {
		conditions = append(conditions, fmt.Sprintf("title = $%d", argCount))
		args = append(args, *filters.Title)
		argCount++
	}

	if filters.TenantID != nil {
		conditions = append(conditions, fmt.Sprintf("tenant_id = $%d", argCount))
		args = append(args, *filters.TenantID)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	return whereClause, args
}

// List retrieves topics with optional filters
func (r *TopicsRepository) List(ctx context.Context, filters *models.ListTopicsFilters) ([]models.Topic, error) {
	query := `
		SELECT id, title, level, parent_id, tenant_id, created_at, updated_at
		FROM topics
	`

	whereClause, args := buildTopicsFilterConditions(filters)
	query += whereClause
	argCount := len(args) + 1

	query += " ORDER BY created_at DESC"

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
		return nil, fmt.Errorf("failed to list topics: %w", err)
	}
	defer rows.Close()

	topics := []models.Topic{} // Initialize as empty slice, not nil
	for rows.Next() {
		var topic models.Topic
		err := rows.Scan(
			&topic.ID, &topic.Title, &topic.Level, &topic.ParentID, &topic.TenantID, &topic.CreatedAt, &topic.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan topic: %w", err)
		}
		topics = append(topics, topic)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating topics: %w", err)
	}

	return topics, nil
}

// Count returns the total count of topics matching the filters
func (r *TopicsRepository) Count(ctx context.Context, filters *models.ListTopicsFilters) (int64, error) {
	query := `SELECT COUNT(*) FROM topics`

	whereClause, args := buildTopicsFilterConditions(filters)
	query += whereClause

	var count int64
	err := r.db.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count topics: %w", err)
	}

	return count, nil
}

// Update updates an existing topic
// Only title can be updated
func (r *TopicsRepository) Update(ctx context.Context, id uuid.UUID, req *models.UpdateTopicRequest) (*models.Topic, error) {
	// If no title provided, just return the existing topic
	if req.Title == nil {
		return r.GetByID(ctx, id)
	}

	query := `
		UPDATE topics
		SET title = $1, updated_at = $2
		WHERE id = $3
		RETURNING id, title, level, parent_id, tenant_id, created_at, updated_at
	`

	var topic models.Topic
	err := r.db.QueryRow(ctx, query, *req.Title, time.Now(), id).Scan(
		&topic.ID, &topic.Title, &topic.Level, &topic.ParentID, &topic.TenantID, &topic.CreatedAt, &topic.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.NewNotFoundError("topic", "topic not found")
		}
		// Check for unique constraint violation
		if strings.Contains(err.Error(), "duplicate key value violates unique constraint") ||
			strings.Contains(err.Error(), "23505") {
			return nil, apperrors.NewConflictError("topic", "topic with this title already exists")
		}
		return nil, fmt.Errorf("failed to update topic: %w", err)
	}

	return &topic, nil
}

// Delete removes a topic (CASCADE handled by FK constraint)
func (r *TopicsRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM topics WHERE id = $1`

	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete topic: %w", err)
	}

	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("topic", "topic not found")
	}

	return nil
}

// ExistsByTitleAndLevel checks if a topic with the given title exists at the given level and tenant
func (r *TopicsRepository) ExistsByTitleAndLevel(ctx context.Context, title string, level int, tenantID *string) (bool, error) {
	var query string
	var args []interface{}

	if tenantID == nil {
		query = `SELECT EXISTS(SELECT 1 FROM topics WHERE title = $1 AND level = $2 AND tenant_id IS NULL)`
		args = []interface{}{title, level}
	} else {
		query = `SELECT EXISTS(SELECT 1 FROM topics WHERE title = $1 AND level = $2 AND tenant_id = $3)`
		args = []interface{}{title, level, *tenantID}
	}

	var exists bool
	err := r.db.QueryRow(ctx, query, args...).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check topic existence: %w", err)
	}

	return exists, nil
}

// ExistsByTitleAndLevelExcluding checks if a topic with the given title exists at the given level and tenant,
// excluding a specific topic ID (used for update uniqueness validation)
func (r *TopicsRepository) ExistsByTitleAndLevelExcluding(ctx context.Context, title string, level int, tenantID *string, excludeID uuid.UUID) (bool, error) {
	var query string
	var args []interface{}

	if tenantID == nil {
		query = `SELECT EXISTS(SELECT 1 FROM topics WHERE title = $1 AND level = $2 AND tenant_id IS NULL AND id != $3)`
		args = []interface{}{title, level, excludeID}
	} else {
		query = `SELECT EXISTS(SELECT 1 FROM topics WHERE title = $1 AND level = $2 AND tenant_id = $3 AND id != $4)`
		args = []interface{}{title, level, *tenantID, excludeID}
	}

	var exists bool
	err := r.db.QueryRow(ctx, query, args...).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check topic existence: %w", err)
	}

	return exists, nil
}

// UpdateEmbedding updates the embedding vector for a topic
func (r *TopicsRepository) UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error {
	query := `
		UPDATE topics
		SET embedding = $1, updated_at = $2
		WHERE id = $3
	`

	result, err := r.db.Exec(ctx, query, pgvector.NewVector(embedding), time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update topic embedding: %w", err)
	}

	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("topic", "topic not found")
	}

	return nil
}

// FindSimilarTopic finds the most similar topic to the given embedding vector.
// Returns nil if no topics with embeddings exist or similarity is below threshold.
// If level is provided, only searches topics at that level.
func (r *TopicsRepository) FindSimilarTopic(ctx context.Context, embedding []float32, tenantID *string, level *int, minSimilarity float64) (*models.TopicMatch, error) {
	query := `
		SELECT id, title, level, 1 - (embedding <=> $1::vector) as similarity
		FROM topics
		WHERE embedding IS NOT NULL
		  AND ($2::varchar IS NULL OR tenant_id = $2)
		  AND ($3::int IS NULL OR level = $3)
		ORDER BY similarity DESC
		LIMIT 1
	`

	var match models.TopicMatch
	err := r.db.QueryRow(ctx, query, pgvector.NewVector(embedding), tenantID, level).Scan(
		&match.TopicID, &match.Title, &match.Level, &match.Similarity,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // No topics with embeddings found
		}
		return nil, fmt.Errorf("failed to find similar topic: %w", err)
	}

	// Return nil if similarity is below threshold
	if match.Similarity < minSimilarity {
		return nil, nil
	}

	return &match, nil
}

// GetChildTopics retrieves Level 2 topics that are children of a given Level 1 topic
func (r *TopicsRepository) GetChildTopics(ctx context.Context, parentID uuid.UUID, tenantID *string, limit int) ([]models.Topic, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT id, title, level, parent_id, tenant_id, created_at, updated_at
		FROM topics
		WHERE parent_id = $1
		  AND ($2::varchar IS NULL OR tenant_id = $2)
		ORDER BY title ASC
		LIMIT $3
	`

	rows, err := r.db.Query(ctx, query, parentID, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get child topics: %w", err)
	}
	defer rows.Close()

	topics := []models.Topic{}
	for rows.Next() {
		var topic models.Topic
		if err := rows.Scan(&topic.ID, &topic.Title, &topic.Level, &topic.ParentID, &topic.TenantID, &topic.CreatedAt, &topic.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan child topic: %w", err)
		}
		topics = append(topics, topic)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating child topics: %w", err)
	}

	return topics, nil
}
