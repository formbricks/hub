package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

const (
	// hnswEfSearch increases HNSW graph traversal candidates (default 40); higher improves recall.
	hnswEfSearch = 200
	// hnswIterativeScanMode makes the HNSW scan resume past ef_search candidates until the query's
	// LIMIT is satisfied (pgvector >= 0.8). Without it, the index emits at most ef_search rows
	// BEFORE the model/tenant post-filters, so a tenant sharing an index with larger tenants gets
	// short or empty pages and pagination silently caps at ~ef_search total results. strict_order
	// preserves exact distance ordering, which the keyset cursor relies on.
	hnswIterativeScanMode = "strict_order"
	// nearestOverFetchFactor: request this many times the requested limit, then filter by minScore in Go,
	// so that after tenant/minScore filtering we still have enough results (avoids iterative scan being blocked by WHERE score threshold).
	nearestOverFetchFactor = 3
	// maxNearestFetchLimit caps over-fetched rows per query to avoid excessive memory/scan.
	maxNearestFetchLimit = 2000
)

var (
	// ErrEmbeddingDimensionMismatch is returned when an embedding slice length does not match EmbeddingVectorDimensions.
	ErrEmbeddingDimensionMismatch = errors.New("embedding dimension mismatch")

	errNoCurrentEmbeddingModels = errors.New("at least one current embedding model is required")
)

// EmbeddingsRepository handles data access for the embeddings table.
type EmbeddingsRepository struct {
	db *pgxpool.Pool
	// iterativeScanUnavailable latches when the server rejects hnsw.iterative_scan (pgvector
	// < 0.8), so searches fall back to the plain ef_search-bounded scan instead of failing.
	iterativeScanUnavailable atomic.Bool
	iterativeScanWarn        sync.Once
}

// NewEmbeddingsRepository creates a new embeddings repository.
func NewEmbeddingsRepository(db *pgxpool.Pool) *EmbeddingsRepository {
	return &EmbeddingsRepository{db: db}
}

// IterativeScanDegraded reports whether HNSW iterative_scan has been latched off after the server
// rejected it (pgvector < 0.8). While true, nearest-neighbor recall is capped at ef_search until
// the process restarts. Surfaced as a gauge so the silent degradation is alertable, not just a
// one-time log line.
func (r *EmbeddingsRepository) IterativeScanDegraded() bool {
	return r.iterativeScanUnavailable.Load()
}

// rollbackQuietly rolls back tx, logging (rather than returning) an unexpected rollback error.
func rollbackQuietly(ctx context.Context, tx pgx.Tx, msg string) {
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Error(msg, "error", err)
	}
}

// Upsert inserts or updates the embedding for (feedback_record_id, model). On conflict updates embedding and updated_at.
// Uses halfvec storage (2 bytes per dimension); pgvector-go converts float32 to float16 when encoding.
// embedding must have length models.EmbeddingVectorDimensions (fixed 768).
//
// stillCurrent (optional) guards against the concurrent-jobs race: two jobs for the same record
// run in parallel and the one that read OLDER content lands its write LAST, permanently attaching
// a stale vector (the missing-rows-only backfill can never repair it). Under a per-record
// advisory lock the record's current content is re-read and compared; a mismatch returns
// huberrors.ErrEmbeddingSuperseded — a benign skip, since the job holding the current content
// writes the row.
func (r *EmbeddingsRepository) Upsert(
	ctx context.Context, feedbackRecordID uuid.UUID, model string, embedding []float32,
	stillCurrent func(fieldLabel, valueText, valueTextTranslated *string) bool,
) error {
	if len(embedding) != models.EmbeddingVectorDimensions {
		return fmt.Errorf("%w: got %d, want %d", ErrEmbeddingDimensionMismatch, len(embedding), models.EmbeddingVectorDimensions)
	}

	vec := pgvector.NewHalfVector(embedding)
	now := time.Now()

	return withTenantWritePoolTx(ctx, r.db, nil, func(dbTx tenantWriteTx) error {
		// Embeddings derive their tenant boundary from the parent feedback
		// record; resolve and lock it so embedding writes cannot race a tenant
		// data purge. A missing parent means the record was deleted or purged.
		if _, err := lockFeedbackRecordTenantShared(ctx, dbTx, feedbackRecordID); err != nil {
			return err
		}

		if err := guardEmbeddingSourceCurrent(ctx, dbTx, feedbackRecordID, stillCurrent); err != nil {
			return err
		}

		_, err := dbTx.Exec(ctx, `
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
	})
}

// DeleteByFeedbackRecordAndModel removes the embedding row for the given feedback record and model.
// stillCurrent (optional) has the same stale-write guard semantics as Upsert: a clear enqueued for
// since-changed content must not delete the vector a newer job wrote.
func (r *EmbeddingsRepository) DeleteByFeedbackRecordAndModel(
	ctx context.Context, feedbackRecordID uuid.UUID, model string,
	stillCurrent func(fieldLabel, valueText, valueTextTranslated *string) bool,
) error {
	return withTenantWritePoolTx(ctx, r.db, nil, func(dbTx tenantWriteTx) error {
		if _, err := lockFeedbackRecordTenantShared(ctx, dbTx, feedbackRecordID); err != nil {
			return err
		}

		if err := guardEmbeddingSourceCurrent(ctx, dbTx, feedbackRecordID, stillCurrent); err != nil {
			return err
		}

		_, err := dbTx.Exec(ctx,
			`DELETE FROM embeddings WHERE feedback_record_id = $1 AND model = $2`,
			feedbackRecordID, model,
		)
		if err != nil {
			return fmt.Errorf("embeddings delete: %w", err)
		}

		return nil
	})
}

// guardEmbeddingSourceCurrent serializes same-record embedding writes (per-record advisory
// transaction lock — a hash collision merely over-serializes) and re-reads the record's current
// content for the stillCurrent check. Returns ErrEmbeddingSuperseded when the content moved on,
// NotFound when the record vanished mid-transaction, and nil (no-op) when stillCurrent is nil.
func guardEmbeddingSourceCurrent(
	ctx context.Context, dbTx tenantWriteTx, feedbackRecordID uuid.UUID,
	stillCurrent func(fieldLabel, valueText, valueTextTranslated *string) bool,
) error {
	if stillCurrent == nil {
		return nil
	}

	if _, err := dbTx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, feedbackRecordID,
	); err != nil {
		return fmt.Errorf("lock feedback record for embedding write: %w", err)
	}

	var fieldLabel, valueText, valueTextTranslated *string

	// FOR UPDATE row-locks the record until this write commits, so a concurrent PATCH Update
	// (which does not take the per-record advisory lock) cannot change the content between this
	// re-read and the embedding write — closing the check-then-write window like the classify
	// sibling guardValueTextCurrent. Crucial here: embeddings have no eager-clear or NULL-rows
	// backfill, so a stale vector that lands last is otherwise permanent.
	err := dbTx.QueryRow(ctx,
		`SELECT field_label, value_text, value_text_translated FROM feedback_records WHERE id = $1 FOR UPDATE`, feedbackRecordID,
	).Scan(&fieldLabel, &valueText, &valueTextTranslated)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return huberrors.NewNotFoundError("feedback record", "feedback record not found")
		}

		return fmt.Errorf("read feedback record content for embedding write: %w", err)
	}

	if !stillCurrent(fieldLabel, valueText, valueTextTranslated) {
		return huberrors.ErrEmbeddingSuperseded
	}

	return nil
}

// DeleteEmbeddingsForOtherModels batch-deletes embedding rows whose model is not in the
// current model set. Reads only ever join on active models, so such rows are dead weight.
// Batched (batchSize rows per DELETE) so a large prune never holds long row locks or
// produces one giant WAL burst. Returns the total deleted. Run only after a model
// migration's backfill has completed, or reads using that model go dark until the new
// model's vectors exist.
func (r *EmbeddingsRepository) DeleteEmbeddingsForOtherModels(
	ctx context.Context, currentModel string, batchSize int, additionalCurrentModels ...string,
) (int64, error) {
	currentModels := normalizeEmbeddingModels(append([]string{currentModel}, additionalCurrentModels...))
	if len(currentModels) == 0 {
		return 0, errNoCurrentEmbeddingModels
	}

	var total int64

	for {
		tag, err := r.db.Exec(ctx, `
			DELETE FROM embeddings WHERE id IN (
				SELECT id FROM embeddings WHERE NOT (model = ANY($1::text[])) LIMIT $2
			)`, currentModels, batchSize)
		if err != nil {
			return total, fmt.Errorf("delete stale-model embeddings: %w", err)
		}

		deleted := tag.RowsAffected()
		total += deleted

		if deleted < int64(batchSize) {
			return total, nil
		}
	}
}

func normalizeEmbeddingModels(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))

	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}

		if _, ok := seen[model]; ok {
			continue
		}

		seen[model] = struct{}{}
		out = append(out, model)
	}

	return out
}

// ListFeedbackRecordIDsForBackfill returns one keyset page (fr.id > afterID, ordered by id,
// at most limit rows) of feedback-record IDs that have non-empty value_text and no row in
// embeddings for the given model (so they need an embedding for that model). Pass uuid.Nil
// as afterID for the first page.
func (r *EmbeddingsRepository) ListFeedbackRecordIDsForBackfill(
	ctx context.Context, model string, afterID uuid.UUID, limit int,
) ([]uuid.UUID, error) {
	return r.ListFeedbackRecordIDsForBackfillByInputKind(ctx, model, models.EmbeddingInputKindRaw, afterID, limit)
}

// ListFeedbackRecordIDsForBackfillByInputKind returns feedback-record IDs missing an embedding
// for model and eligible for the requested embedding input kind.
func (r *EmbeddingsRepository) ListFeedbackRecordIDsForBackfillByInputKind(
	ctx context.Context,
	model string,
	inputKind models.EmbeddingInputKind,
	afterID uuid.UUID,
	limit int,
) ([]uuid.UUID, error) {
	query := `
		SELECT fr.id FROM feedback_records fr
		WHERE fr.value_text IS NOT NULL AND trim(fr.value_text) != ''
		  AND fr.id > $2
		  AND NOT EXISTS (
		    SELECT 1 FROM embeddings e
		    WHERE e.feedback_record_id = fr.id AND e.model = $1
		  )
		ORDER BY fr.id
		LIMIT $3
	`
	if models.NormalizeEmbeddingInputKind(inputKind) == models.EmbeddingInputKindTaxonomyTranslated {
		query = `
			SELECT fr.id FROM feedback_records fr
			WHERE COALESCE(NULLIF(btrim(fr.value_text_translated), ''), NULLIF(btrim(fr.value_text), '')) IS NOT NULL
			  AND fr.id > $2
			  AND NOT EXISTS (
			    SELECT 1 FROM embeddings e
			    WHERE e.feedback_record_id = fr.id AND e.model = $1
			  )
			ORDER BY fr.id
			LIMIT $3
		`
	}

	rows, err := r.db.Query(ctx, query, model, afterID, limit)
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
	embedding, _, err := r.GetEmbeddingAndTenantByFeedbackRecordAndModel(ctx, feedbackRecordID, model)
	if err != nil {
		return nil, err
	}

	return embedding, nil
}

// GetEmbeddingAndTenantByFeedbackRecordAndModel returns the stored embedding and its feedback record tenant.
// Used by record-level similar feedback so the source record determines the tenant boundary for the search.
// Returns ErrEmbeddingNotFound when no embedding exists for the current model.
func (r *EmbeddingsRepository) GetEmbeddingAndTenantByFeedbackRecordAndModel(
	ctx context.Context, feedbackRecordID uuid.UUID, model string,
) ([]float32, string, error) {
	var (
		vec      pgvector.HalfVector
		tenantID string
	)

	err := r.db.QueryRow(ctx,
		`SELECT e.embedding, fr.tenant_id FROM embeddings e
		 INNER JOIN feedback_records fr ON fr.id = e.feedback_record_id
		 WHERE e.feedback_record_id = $1 AND e.model = $2`,
		feedbackRecordID, model,
	).Scan(&vec, &tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrEmbeddingNotFound
		}

		return nil, "", fmt.Errorf("get embedding and tenant: %w", err)
	}

	return vec.Slice(), tenantID, nil
}

// NearestFeedbackRecordsByEmbedding returns feedback record IDs and similarity scores (0..1) for the
// nearest neighbors to queryEmbedding, filtered by model and tenant. Rows with score < minScore are
// filtered in application code (not in WHERE) so pgvector's iterative index scan can run. The query
// vector is sent full-precision and implicitly cast to halfvec by the <=> operator (that cast is
// what makes the halfvec index usable). Sets hnsw.ef_search and iterative scan for recall.
// Over-fetches then trims to limit to account for tenant/minScore filtering. excludeID optionally
// excludes one feedback record (e.g. for "similar" endpoint). First page only; use
// NearestFeedbackRecordsByEmbeddingAfterCursor for next pages.
func (r *EmbeddingsRepository) NearestFeedbackRecordsByEmbedding(
	ctx context.Context, model string, queryEmbedding []float32, tenantID string, limit int, excludeID *uuid.UUID, minScore float64,
) ([]models.FeedbackRecordWithScore, bool, error) {
	if len(queryEmbedding) != models.EmbeddingVectorDimensions {
		return nil, false, fmt.Errorf("%w: got %d, want %d", ErrEmbeddingDimensionMismatch, len(queryEmbedding), models.EmbeddingVectorDimensions)
	}

	queryVec := pgvector.NewVector(queryEmbedding)

	fetchLimit := min(limit*nearestOverFetchFactor, maxNearestFetchLimit)

	dbTx, err := r.beginNearestTx(ctx)
	if err != nil {
		return nil, false, err
	}

	defer rollbackQuietly(ctx, dbTx, "nearest feedback records: rollback failed")

	var rows pgx.Rows
	if excludeID == nil {
		rows, err = dbTx.Query(ctx, `
			SELECT e.feedback_record_id, (e.embedding <=> $1) AS distance,
				COALESCE(fr.field_label, ''), fr.value_text
			FROM embeddings e
			INNER JOIN feedback_records fr ON fr.id = e.feedback_record_id
			WHERE e.model = $2 AND fr.tenant_id = $3
			  AND e.model NOT LIKE 'taxonomy:%'
			ORDER BY (e.embedding <=> $1), e.feedback_record_id
			LIMIT $4`, queryVec, model, tenantID, fetchLimit)
	} else {
		rows, err = dbTx.Query(ctx, `
			SELECT e.feedback_record_id, (e.embedding <=> $1) AS distance,
				COALESCE(fr.field_label, ''), fr.value_text
			FROM embeddings e
			INNER JOIN feedback_records fr ON fr.id = e.feedback_record_id
			WHERE e.model = $2 AND fr.tenant_id = $3 AND e.feedback_record_id != $4
			  AND e.model NOT LIKE 'taxonomy:%'
			ORDER BY (e.embedding <=> $1), e.feedback_record_id
			LIMIT $5`, queryVec, model, tenantID, *excludeID, fetchLimit)
	}

	if err != nil {
		return nil, false, fmt.Errorf("nearest feedback records: %w", err)
	}

	defer rows.Close()

	var (
		results  []models.FeedbackRecordWithScore
		rowCount int
	)

	brokeWithFullPage := false

	for rows.Next() {
		rowCount++

		var (
			row       models.FeedbackRecordWithScore
			valueText *string
		)
		if err := rows.Scan(&row.FeedbackRecordID, &row.Distance, &row.FieldLabel, &valueText); err != nil {
			return nil, false, fmt.Errorf("scan feedback record with score: %w", err)
		}

		if valueText != nil {
			row.ValueText = *valueText
		}

		// Derive the display score in Go rather than in SQL: computing it there evaluated the
		// <=> distance operator a second time per row. Distance is what the query orders/cursors by.
		row.Score = 1 - row.Distance

		if row.Score >= minScore {
			results = append(results, row)
			if len(results) >= limit {
				brokeWithFullPage = true

				break
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterating nearest: %w", err)
	}

	// Close rows before Commit so the connection is not busy (avoids "conn busy" when breaking early from the loop).
	rows.Close()

	if err := dbTx.Commit(ctx); err != nil {
		slog.Error("nearest feedback records: commit failed", "error", err)

		return nil, false, fmt.Errorf("commit: %w", err)
	}

	hasMore := brokeWithFullPage || rowCount >= fetchLimit

	return results, hasMore, nil
}

// NearestFeedbackRecordsByEmbeddingAfterCursor returns the next page of nearest neighbors after the given
// cursor (lastDistance, lastFeedbackRecordID). Order is by (distance ASC, feedback_record_id ASC). minScore
// is applied in application code; query settings match NearestFeedbackRecordsByEmbedding. The cursor's
// lastDistance is the exact distance the previous page selected (not re-derived from the score), so the
// keyset comparison matches the stored ordering bit-for-bit.
func (r *EmbeddingsRepository) NearestFeedbackRecordsByEmbeddingAfterCursor(
	ctx context.Context, model string, queryEmbedding []float32, tenantID string, limit int,
	lastDistance float64, lastFeedbackRecordID uuid.UUID, excludeID *uuid.UUID, minScore float64,
) ([]models.FeedbackRecordWithScore, bool, error) {
	if len(queryEmbedding) != models.EmbeddingVectorDimensions {
		return nil, false, fmt.Errorf("%w: got %d, want %d", ErrEmbeddingDimensionMismatch, len(queryEmbedding), models.EmbeddingVectorDimensions)
	}

	queryVec := pgvector.NewVector(queryEmbedding)

	fetchLimit := min(limit*nearestOverFetchFactor, maxNearestFetchLimit)

	dbTx, err := r.beginNearestTx(ctx)
	if err != nil {
		return nil, false, err
	}

	defer rollbackQuietly(ctx, dbTx, "nearest feedback records after cursor: rollback failed")

	var rows pgx.Rows
	if excludeID == nil {
		rows, err = dbTx.Query(ctx, `
			SELECT e.feedback_record_id, (e.embedding <=> $1) AS distance,
				COALESCE(fr.field_label, ''), fr.value_text
			FROM embeddings e
			INNER JOIN feedback_records fr ON fr.id = e.feedback_record_id
			WHERE e.model = $2 AND fr.tenant_id = $3
			  AND e.model NOT LIKE 'taxonomy:%'
			  AND ((e.embedding <=> $1), e.feedback_record_id) > ($4, $5)
			ORDER BY (e.embedding <=> $1), e.feedback_record_id
			LIMIT $6`, queryVec, model, tenantID, lastDistance, lastFeedbackRecordID, fetchLimit)
	} else {
		rows, err = dbTx.Query(ctx, `
			SELECT e.feedback_record_id, (e.embedding <=> $1) AS distance,
				COALESCE(fr.field_label, ''), fr.value_text
			FROM embeddings e
			INNER JOIN feedback_records fr ON fr.id = e.feedback_record_id
			WHERE e.model = $2 AND fr.tenant_id = $3 AND e.feedback_record_id != $4
			  AND e.model NOT LIKE 'taxonomy:%'
			  AND ((e.embedding <=> $1), e.feedback_record_id) > ($5, $6)
			ORDER BY (e.embedding <=> $1), e.feedback_record_id
			LIMIT $7`, queryVec, model, tenantID, *excludeID, lastDistance, lastFeedbackRecordID, fetchLimit)
	}

	if err != nil {
		return nil, false, fmt.Errorf("nearest feedback records after cursor: %w", err)
	}

	defer rows.Close()

	var (
		results  []models.FeedbackRecordWithScore
		rowCount int
	)

	brokeWithFullPage := false

	for rows.Next() {
		rowCount++

		var (
			row       models.FeedbackRecordWithScore
			valueText *string
		)
		if err := rows.Scan(&row.FeedbackRecordID, &row.Distance, &row.FieldLabel, &valueText); err != nil {
			return nil, false, fmt.Errorf("scan feedback record with score: %w", err)
		}

		if valueText != nil {
			row.ValueText = *valueText
		}

		// Derive the display score in Go rather than in SQL: computing it there evaluated the
		// <=> distance operator a second time per row. Distance is what the query orders/cursors by.
		row.Score = 1 - row.Distance

		if row.Score >= minScore {
			results = append(results, row)
			if len(results) >= limit {
				brokeWithFullPage = true

				break
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterating nearest after cursor: %w", err)
	}

	// Close rows before Commit so the connection is not busy.
	rows.Close()

	if err := dbTx.Commit(ctx); err != nil {
		slog.Error("nearest feedback records after cursor: commit failed", "error", err)

		return nil, false, fmt.Errorf("commit: %w", err)
	}

	hasMore := brokeWithFullPage || rowCount >= fetchLimit

	return results, hasMore, nil
}

// hnswGUCUnsupportedSQLStates are the Postgres error codes for an unknown/invalid configuration
// parameter name: an unrecognized GUC in a reserved prefix (hnsw.*) is reported as 42602
// (invalid_name), and some versions use 42704 (undefined_object). Only these — a genuinely
// version-unsupported GUC (pgvector < 0.8) — should permanently latch the iterative-scan fallback.
var hnswGUCUnsupportedSQLStates = map[string]bool{"42602": true, "42704": true}

// beginNearestTx starts the transaction for a nearest-neighbor query and applies the HNSW query
// settings: ef_search, and iterative scan so the index keeps yielding candidates until LIMIT is
// satisfied rather than stopping at ef_search pre-filter rows. On a server without the
// iterative-scan GUC (pgvector < 0.8) the failed SET aborts the transaction, so it latches the
// fallback, rolls back, and rebuilds the transaction without it (warning once).
func (r *EmbeddingsRepository) beginNearestTx(ctx context.Context) (pgx.Tx, error) {
	dbTx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}

	// SET LOCAL does not support bound parameters; values are package constants.
	if _, err := dbTx.Exec(ctx, fmt.Sprintf("SET LOCAL hnsw.ef_search = %d", hnswEfSearch)); err != nil {
		rollbackQuietly(ctx, dbTx, "nearest feedback records: setup rollback failed")

		return nil, fmt.Errorf("set hnsw.ef_search: %w", err)
	}

	if !r.iterativeScanUnavailable.Load() {
		if _, err := dbTx.Exec(ctx, "SET LOCAL hnsw.iterative_scan = "+hnswIterativeScanMode); err != nil {
			rollbackQuietly(ctx, dbTx, "nearest feedback records: setup rollback failed")

			// Only latch on a genuinely-unsupported GUC (pgvector < 0.8). A transient failure must
			// not permanently degrade recall for the process's life — surface it so this query fails
			// and the next call retries the iterative-scan path.
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || !hnswGUCUnsupportedSQLStates[pgErr.Code] {
				return nil, fmt.Errorf("set hnsw.iterative_scan: %w", err)
			}

			r.iterativeScanUnavailable.Store(true)
			r.iterativeScanWarn.Do(func() {
				slog.Warn("hnsw.iterative_scan unavailable (pgvector < 0.8?); "+
					"semantic search recall is capped at ef_search candidates per query",
					"error", err)
			})

			return r.beginNearestTx(ctx)
		}
	}

	return dbTx, nil
}
