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

// KnowledgeRecordsRepository handles data access for knowledge records
type KnowledgeRecordsRepository struct {
	db *pgxpool.Pool
}

// NewKnowledgeRecordsRepository creates a new knowledge records repository
func NewKnowledgeRecordsRepository(db *pgxpool.Pool) *KnowledgeRecordsRepository {
	return &KnowledgeRecordsRepository{db: db}
}

// normalizeTenantID converts empty string tenant_id to nil for consistency
func normalizeTenantID(tenantID *string) *string {
	if tenantID != nil && *tenantID == "" {
		return nil
	}
	return tenantID
}

// Create inserts a new knowledge record
func (r *KnowledgeRecordsRepository) Create(ctx context.Context, req *models.CreateKnowledgeRecordRequest) (*models.KnowledgeRecord, error) {
	tenantID := normalizeTenantID(req.TenantID)

	query := `
		INSERT INTO knowledge_records (content, tenant_id)
		VALUES ($1, $2)
		RETURNING id, content, tenant_id, created_at, updated_at
	`

	var record models.KnowledgeRecord
	err := r.db.QueryRow(ctx, query, req.Content, tenantID).Scan(
		&record.ID, &record.Content, &record.TenantID, &record.CreatedAt, &record.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create knowledge record: %w", err)
	}

	return &record, nil
}

// GetByID retrieves a single knowledge record by ID
func (r *KnowledgeRecordsRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.KnowledgeRecord, error) {
	query := `
		SELECT id, content, tenant_id, created_at, updated_at
		FROM knowledge_records
		WHERE id = $1
	`

	var record models.KnowledgeRecord
	err := r.db.QueryRow(ctx, query, id).Scan(
		&record.ID, &record.Content, &record.TenantID, &record.CreatedAt, &record.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.NewNotFoundError("knowledge record", "knowledge record not found")
		}
		return nil, fmt.Errorf("failed to get knowledge record: %w", err)
	}

	return &record, nil
}

// buildKnowledgeRecordsFilterConditions builds WHERE clause conditions and arguments from filters
func buildKnowledgeRecordsFilterConditions(filters *models.ListKnowledgeRecordsFilters) (string, []interface{}) {
	var conditions []string
	var args []interface{}
	argCount := 1

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

// List retrieves knowledge records with optional filters
func (r *KnowledgeRecordsRepository) List(ctx context.Context, filters *models.ListKnowledgeRecordsFilters) ([]models.KnowledgeRecord, error) {
	query := `
		SELECT id, content, tenant_id, created_at, updated_at
		FROM knowledge_records
	`

	whereClause, args := buildKnowledgeRecordsFilterConditions(filters)
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
		return nil, fmt.Errorf("failed to list knowledge records: %w", err)
	}
	defer rows.Close()

	records := []models.KnowledgeRecord{} // Initialize as empty slice, not nil
	for rows.Next() {
		var record models.KnowledgeRecord
		err := rows.Scan(
			&record.ID, &record.Content, &record.TenantID, &record.CreatedAt, &record.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan knowledge record: %w", err)
		}
		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating knowledge records: %w", err)
	}

	return records, nil
}

// Count returns the total count of knowledge records matching the filters
func (r *KnowledgeRecordsRepository) Count(ctx context.Context, filters *models.ListKnowledgeRecordsFilters) (int64, error) {
	query := `SELECT COUNT(*) FROM knowledge_records`

	whereClause, args := buildKnowledgeRecordsFilterConditions(filters)
	query += whereClause

	var count int64
	err := r.db.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count knowledge records: %w", err)
	}

	return count, nil
}

// Update updates an existing knowledge record
// Only content can be updated
func (r *KnowledgeRecordsRepository) Update(ctx context.Context, id uuid.UUID, req *models.UpdateKnowledgeRecordRequest) (*models.KnowledgeRecord, error) {
	// If no content provided, just return the existing record
	if req.Content == nil {
		return r.GetByID(ctx, id)
	}

	query := `
		UPDATE knowledge_records
		SET content = $1, updated_at = $2
		WHERE id = $3
		RETURNING id, content, tenant_id, created_at, updated_at
	`

	var record models.KnowledgeRecord
	err := r.db.QueryRow(ctx, query, *req.Content, time.Now(), id).Scan(
		&record.ID, &record.Content, &record.TenantID, &record.CreatedAt, &record.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.NewNotFoundError("knowledge record", "knowledge record not found")
		}
		return nil, fmt.Errorf("failed to update knowledge record: %w", err)
	}

	return &record, nil
}

// Delete removes a knowledge record
func (r *KnowledgeRecordsRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM knowledge_records WHERE id = $1`

	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete knowledge record: %w", err)
	}

	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("knowledge record", "knowledge record not found")
	}

	return nil
}

// BulkDelete deletes all knowledge records matching tenant_id
func (r *KnowledgeRecordsRepository) BulkDelete(ctx context.Context, tenantID string) (int64, error) {
	query := `DELETE FROM knowledge_records WHERE tenant_id = $1`

	result, err := r.db.Exec(ctx, query, tenantID)
	if err != nil {
		return 0, fmt.Errorf("failed to bulk delete knowledge records: %w", err)
	}

	return result.RowsAffected(), nil
}

// UpdateEmbedding updates the embedding vector for a knowledge record
func (r *KnowledgeRecordsRepository) UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error {
	query := `
		UPDATE knowledge_records
		SET embedding = $1, updated_at = $2
		WHERE id = $3
	`

	result, err := r.db.Exec(ctx, query, pgvector.NewVector(embedding), time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update knowledge record embedding: %w", err)
	}

	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("knowledge record", "knowledge record not found")
	}

	return nil
}
