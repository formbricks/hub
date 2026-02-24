package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// EmbeddingsRepository handles data access for the embeddings table.
type EmbeddingsRepository struct {
	db *pgxpool.Pool
}

// NewEmbeddingsRepository creates a new embeddings repository.
func NewEmbeddingsRepository(db *pgxpool.Pool) *EmbeddingsRepository {
	return &EmbeddingsRepository{db: db}
}

// Upsert inserts or updates the embedding for (feedback_record_id, model). On conflict updates embedding and updated_at.
func (r *EmbeddingsRepository) Upsert(
	ctx context.Context, feedbackRecordID uuid.UUID, model string, embedding []float32,
) error {
	vec := pgvector.NewVector(embedding)
	now := time.Now()

	_, err := r.db.Exec(ctx, `
		INSERT INTO embeddings (feedback_record_id, embedding, model, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (feedback_record_id, model)
		DO UPDATE SET embedding = EXCLUDED.embedding, updated_at = $5`,
		feedbackRecordID, vec, model, now, now,
	)
	if err != nil {
		return fmt.Errorf("embeddings upsert: %w", err)
	}

	return nil
}

// DeleteByFeedbackRecordAndModel removes the embedding row for the given feedback record and model.
func (r *EmbeddingsRepository) DeleteByFeedbackRecordAndModel(
	ctx context.Context, feedbackRecordID uuid.UUID, model string,
) error {
	_, err := r.db.Exec(ctx,
		`DELETE FROM embeddings WHERE feedback_record_id = $1 AND model = $2`,
		feedbackRecordID, model,
	)
	if err != nil {
		return fmt.Errorf("embeddings delete: %w", err)
	}

	return nil
}

// ListFeedbackRecordIDsForBackfill returns IDs of feedback records that have non-empty value_text
// and no row in embeddings for the given model (so they need an embedding for that model).
func (r *EmbeddingsRepository) ListFeedbackRecordIDsForBackfill(ctx context.Context, model string) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT fr.id FROM feedback_records fr
		WHERE fr.value_text IS NOT NULL AND trim(fr.value_text) != ''
		  AND NOT EXISTS (
		    SELECT 1 FROM embeddings e
		    WHERE e.feedback_record_id = fr.id AND e.model = $1
		  )
	`, model)
	if err != nil {
		return nil, fmt.Errorf("list feedback record ids for backfill: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID

	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan feedback record id: %w", err)
		}

		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating backfill ids: %w", err)
	}

	return ids, nil
}
