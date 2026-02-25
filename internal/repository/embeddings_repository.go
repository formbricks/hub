package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/formbricks/hub/internal/models"
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
// Uses halfvec storage (2 bytes per dimension); pgvector-go converts float32 to float16 when encoding.
func (r *EmbeddingsRepository) Upsert(
	ctx context.Context, feedbackRecordID uuid.UUID, model string, embedding []float32,
) error {
	vec := pgvector.NewHalfVector(embedding)
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

// ErrEmbeddingNotFound is returned when no embedding row exists for the given feedback record and model.
var ErrEmbeddingNotFound = errors.New("embedding not found for feedback record and model")

// GetEmbeddingByFeedbackRecordAndModel returns the stored embedding for the given feedback record and model.
// Returns ErrEmbeddingNotFound when no row exists (record not embedded yet).
func (r *EmbeddingsRepository) GetEmbeddingByFeedbackRecordAndModel(
	ctx context.Context, feedbackRecordID uuid.UUID, model string,
) ([]float32, error) {
	var vec pgvector.HalfVector

	err := r.db.QueryRow(ctx,
		`SELECT embedding FROM embeddings WHERE feedback_record_id = $1 AND model = $2`,
		feedbackRecordID, model,
	).Scan(&vec)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEmbeddingNotFound
		}

		return nil, fmt.Errorf("get embedding: %w", err)
	}

	return vec.Slice(), nil
}

// NearestFeedbackRecordsByEmbedding returns feedback record IDs and similarity scores (0..1) for the
// nearest neighbors to queryEmbedding, filtered by model and tenant. Only rows with score >= minScore
// are returned. Uses cosine distance (<=>); score = 1 - distance. excludeID optionally excludes one
// feedback record (e.g. for "similar" endpoint).
func (r *EmbeddingsRepository) NearestFeedbackRecordsByEmbedding(
	ctx context.Context, model string, queryEmbedding []float32, tenantID string, limit int, excludeID *uuid.UUID, minScore float64,
) ([]models.FeedbackRecordWithScore, error) {
	queryVec := pgvector.NewHalfVector(queryEmbedding)

	var (
		rows pgx.Rows
		err  error
	)

	if excludeID == nil {
		rows, err = r.db.Query(ctx, `
			SELECT e.feedback_record_id, (1 - (e.embedding <=> $1)) AS score, fr.value_text
			FROM embeddings e
			INNER JOIN feedback_records fr ON fr.id = e.feedback_record_id
			WHERE e.model = $2 AND fr.tenant_id = $3 AND (1 - (e.embedding <=> $1)) >= $4
			ORDER BY e.embedding <=> $1
			LIMIT $5`, queryVec, model, tenantID, minScore, limit)
	} else {
		rows, err = r.db.Query(ctx, `
			SELECT e.feedback_record_id, (1 - (e.embedding <=> $1)) AS score, fr.value_text
			FROM embeddings e
			INNER JOIN feedback_records fr ON fr.id = e.feedback_record_id
			WHERE e.model = $2 AND fr.tenant_id = $3 AND e.feedback_record_id != $4 AND (1 - (e.embedding <=> $1)) >= $5
			ORDER BY e.embedding <=> $1
			LIMIT $6`, queryVec, model, tenantID, *excludeID, minScore, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("nearest feedback records: %w", err)
	}

	defer rows.Close()

	var results []models.FeedbackRecordWithScore

	for rows.Next() {
		var (
			row       models.FeedbackRecordWithScore
			valueText *string
		)

		if err := rows.Scan(&row.FeedbackRecordID, &row.Score, &valueText); err != nil {
			return nil, fmt.Errorf("scan feedback record with score: %w", err)
		}

		if valueText != nil {
			row.ValueText = *valueText
		}

		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating nearest: %w", err)
	}

	return results, nil
}
