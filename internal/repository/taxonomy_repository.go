package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/jsonutil"
	"github.com/formbricks/hub/internal/models"
)

const (
	defaultTaxonomyRunsLimit       = 20
	defaultTaxonomyNodeRecordLimit = 50
)

var (
	defaultJSONObj = json.RawMessage(`{}`)
	defaultJSONArr = json.RawMessage(`[]`)
)

const taxonomyRunInputChangedMessage = "a selected feedback record was deleted after taxonomy dispatch"

// TaxonomyRepository stores taxonomy runs, artifacts, and edit events.
type TaxonomyRepository struct {
	db *pgxpool.Pool
}

// CreateTaxonomyRunParams contains the data needed to create a taxonomy run.
type CreateTaxonomyRunParams struct {
	models.TaxonomyScope

	FieldLabel     *string
	Params         json.RawMessage
	RecordCount    int
	EmbeddingCount int
}

// NewTaxonomyRepository creates a taxonomy repository.
func NewTaxonomyRepository(db *pgxpool.Pool) *TaxonomyRepository {
	return &TaxonomyRepository{db: db}
}

// ListFieldOptions returns taxonomy-capable feedback fields for a tenant.
func (r *TaxonomyRepository) ListFieldOptions(
	ctx context.Context,
	tenantID string,
	embeddingModel string,
) ([]models.TaxonomyFieldOption, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			fr.tenant_id,
			fr.source_type,
			COALESCE(NULLIF(btrim(fr.source_id), ''), ''),
			COALESCE(MAX(fr.source_name) FILTER (WHERE fr.source_name IS NOT NULL AND btrim(fr.source_name) <> ''), ''),
			fr.field_id,
			COALESCE(MAX(fr.field_label) FILTER (WHERE fr.field_label IS NOT NULL AND btrim(fr.field_label) <> ''), ''),
			COUNT(*)::int,
			COUNT(e.feedback_record_id)::int
		FROM feedback_records fr
		LEFT JOIN embeddings e ON e.feedback_record_id = fr.id AND e.model = $2
		WHERE fr.tenant_id = $1
		  AND COALESCE(NULLIF(btrim(fr.value_text_translated), ''), NULLIF(btrim(fr.value_text), '')) IS NOT NULL
		GROUP BY fr.tenant_id, fr.source_type, COALESCE(NULLIF(btrim(fr.source_id), ''), ''), fr.field_id
		ORDER BY fr.source_type, COALESCE(NULLIF(btrim(fr.source_id), ''), ''), fr.field_id`,
		tenantID, embeddingModel,
	)
	if err != nil {
		return nil, fmt.Errorf("list taxonomy field options: %w", err)
	}
	defer rows.Close()

	out := make([]models.TaxonomyFieldOption, 0)

	for rows.Next() {
		var option models.TaxonomyFieldOption
		if err := rows.Scan(
			&option.TenantID,
			&option.SourceType,
			&option.SourceID,
			&option.SourceName,
			&option.FieldID,
			&option.FieldLabel,
			&option.RecordCount,
			&option.EmbeddingCount,
		); err != nil {
			return nil, fmt.Errorf("scan taxonomy field option: %w", err)
		}

		out = append(out, option)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate taxonomy field options: %w", err)
	}

	return out, nil
}

// CountScopeInput counts text records and embeddings for a taxonomy scope.
func (r *TaxonomyRepository) CountScopeInput(
	ctx context.Context,
	scope models.TaxonomyScope,
	embeddingModel string,
) (int, int, *string, error) {
	var (
		recordCount    int
		embeddingCount int
		fieldLabel     *string
	)

	if taxonomyScopeType(scope) == models.TaxonomyScopeTypeDirectory {
		err := r.db.QueryRow(ctx, `
			SELECT
				COUNT(*)::int,
				COUNT(e.feedback_record_id)::int
			FROM feedback_records fr
			LEFT JOIN embeddings e ON e.feedback_record_id = fr.id AND e.model = $2
			WHERE fr.tenant_id = $1
			  AND COALESCE(NULLIF(btrim(fr.value_text_translated), ''), NULLIF(btrim(fr.value_text), '')) IS NOT NULL`,
			scope.TenantID, embeddingModel,
		).Scan(&recordCount, &embeddingCount)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("count directory taxonomy input: %w", err)
		}

		return recordCount, embeddingCount, nil, nil
	}

	err := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*)::int,
			COUNT(e.feedback_record_id)::int,
			MAX(fr.field_label) FILTER (WHERE fr.field_label IS NOT NULL AND btrim(fr.field_label) <> '')
		FROM feedback_records fr
		LEFT JOIN embeddings e ON e.feedback_record_id = fr.id AND e.model = $5
		WHERE fr.tenant_id = $1
		  AND fr.source_type = $2
		  AND NULLIF(btrim(fr.source_id), '') IS NOT DISTINCT FROM NULLIF(btrim($3), '')
		  AND fr.field_id = $4
		  AND COALESCE(NULLIF(btrim(fr.value_text_translated), ''), NULLIF(btrim(fr.value_text), '')) IS NOT NULL`,
		scope.TenantID, scope.SourceType, scope.SourceID, scope.FieldID, embeddingModel,
	).Scan(&recordCount, &embeddingCount, &fieldLabel)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("count taxonomy scope input: %w", err)
	}

	return recordCount, embeddingCount, fieldLabel, nil
}

// CreateRunIfAvailable creates a taxonomy run unless one is already pending or running.
func (r *TaxonomyRepository) CreateRunIfAvailable(
	ctx context.Context,
	params CreateTaxonomyRunParams,
) (*models.TaxonomyRun, bool, error) {
	var (
		run     *models.TaxonomyRun
		created bool
	)

	err := withTenantWritePoolTx(ctx, r.db, []string{params.TenantID}, func(dbTx tenantWriteTx) error {
		scopeType := taxonomyScopeType(params.TaxonomyScope)
		// Scope lock comes second, after the shared tenant write lock, per the
		// tenant write lock-order convention (see tenant_write_lock.go). Scope
		// waiters already hold the tenant lock in shared mode, so they never
		// block a same-tenant writer and always yield to a queued purge.
		lockKey := taxonomyScopeLockKey(params.TaxonomyScope)
		if _, err := dbTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
			return fmt.Errorf("lock taxonomy run scope: %w", err)
		}

		existing, err := queryTaxonomyRun(ctx, dbTx, taxonomyRunSelect+`
			FROM taxonomy_runs
			WHERE tenant_id = $1
			  AND scope_type = $2
			  AND source_type = $3
			  AND source_id = $4
			  AND field_id = $5
			  AND status IN ('pending', 'running')
			ORDER BY created_at DESC, id DESC
			LIMIT 1`,
			params.TenantID, scopeType, params.SourceType, params.SourceID, params.FieldID,
		)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("find in-progress taxonomy run: %w", err)
		}

		if existing != nil {
			run = existing

			return nil
		}

		inserted, err := queryTaxonomyRun(ctx, dbTx, `
			WITH taxonomy_runs AS (
				INSERT INTO taxonomy_runs (
					tenant_id, scope_type, source_type, source_id, field_id, field_label,
					status, params, record_count, embedding_count
				)
				VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8, $9)
				RETURNING *
			)`+taxonomyRunSelect+` FROM taxonomy_runs`,
			params.TenantID,
			scopeType,
			params.SourceType,
			params.SourceID,
			params.FieldID,
			params.FieldLabel,
			rawOrDefault(params.Params, defaultJSONObj),
			params.RecordCount,
			params.EmbeddingCount,
		)
		if err != nil {
			return fmt.Errorf("insert taxonomy run: %w", err)
		}

		run = inserted
		created = true

		return nil
	})
	if err != nil {
		return nil, false, err
	}

	return run, created, nil
}

func taxonomyScopeLockKey(scope models.TaxonomyScope) string {
	return fmt.Sprintf(
		"%d:%s|%d:%s|%d:%s|%d:%s|%d:%s",
		len(taxonomyScopeType(scope)), taxonomyScopeType(scope),
		len(scope.TenantID), scope.TenantID,
		len(scope.SourceType), scope.SourceType,
		len(scope.SourceID), scope.SourceID,
		len(scope.FieldID), scope.FieldID,
	)
}

func taxonomyScopeType(scope models.TaxonomyScope) models.TaxonomyScopeType {
	if scope.ScopeType == "" {
		return models.TaxonomyScopeTypeField
	}

	return scope.ScopeType
}

// MarkRunRunning transitions a taxonomy run to running.
func (r *TaxonomyRepository) MarkRunRunning(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
) (*models.TaxonomyRun, error) {
	var run *models.TaxonomyRun

	err := withTenantWritePoolTx(ctx, r.db, []string{tenantID}, func(dbTx tenantWriteTx) error {
		updated, err := queryTaxonomyRun(ctx, dbTx, `
			WITH taxonomy_runs AS (
				UPDATE taxonomy_runs
				SET status = 'running', started_at = COALESCE(started_at, NOW()), updated_at = NOW()
				WHERE id = $1 AND tenant_id = $2 AND status = 'pending'
				RETURNING *
			)`+taxonomyRunSelect+` FROM taxonomy_runs`,
			runID, tenantID,
		)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return r.transitionError(ctx, dbTx, runID, tenantID, models.TaxonomyRunStatusRunning)
			}

			return fmt.Errorf("mark taxonomy run running: %w", err)
		}

		run = updated

		return nil
	})
	if err != nil {
		return nil, err
	}

	return run, nil
}

// MarkRunFailed transitions a taxonomy run to failed with an error message.
func (r *TaxonomyRepository) MarkRunFailed(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
	message string,
	errorCode models.TaxonomyRunFailureCode,
	metrics json.RawMessage,
) (*models.TaxonomyRun, error) {
	var run *models.TaxonomyRun

	err := withTenantWritePoolTx(ctx, r.db, []string{tenantID}, func(dbTx tenantWriteTx) error {
		normalizedMetrics := rawOrDefault(metrics, defaultJSONObj)

		existing, err := queryTaxonomyRun(ctx, dbTx, taxonomyRunSelect+`
			FROM taxonomy_runs
			WHERE id = $1 AND tenant_id = $2
			FOR UPDATE`, runID, tenantID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return huberrors.NewNotFoundError("taxonomy_run", "taxonomy run not found")
			}

			return fmt.Errorf("lock taxonomy run for failure: %w", err)
		}

		if existing.Status == models.TaxonomyRunStatusFailed && taxonomyFailureCallbackMatches(
			existing, message, errorCode, normalizedMetrics,
		) {
			run = existing

			return nil
		}

		if existing.Status != models.TaxonomyRunStatusPending && existing.Status != models.TaxonomyRunStatusRunning {
			return taxonomyTransitionConflict(existing, models.TaxonomyRunStatusFailed)
		}

		updated, err := queryTaxonomyRun(ctx, dbTx, `
			WITH taxonomy_runs AS (
				UPDATE taxonomy_runs
				SET status = 'failed', error = $2, error_code = $3, metrics = $4,
					finished_at = NOW(), updated_at = NOW()
				WHERE id = $1 AND tenant_id = $5
				RETURNING *
			)`+taxonomyRunSelect+` FROM taxonomy_runs`,
			runID, message, nullableFailureCode(errorCode), normalizedMetrics, tenantID,
		)
		if err != nil {
			return fmt.Errorf("mark taxonomy run failed: %w", err)
		}

		run = updated

		return nil
	})
	if err != nil {
		return nil, err
	}

	return run, nil
}

// Heartbeat bumps updated_at to NOW() for a run still in a non-terminal state (pending/running),
// keeping it out of the stuck-run reaper's reach for another timeout window. The taxonomy service
// calls this periodically while a generation is in flight; updated_at is the liveness signal the
// reaper (FailStuckRuns) checks against.
//
// The update runs through the shared tenant write lock — the repository invariant that coordinates
// tenant-owned writes with tenant-data purges. A heartbeat that finds no pending/running row (the run
// finished, was purged, or its id is stale) is a benign no-op: there is nothing left to keep alive, so
// it succeeds silently rather than erroring. Callers resolve the run (and thus a 404 for an unknown
// id) before reaching here.
func (r *TaxonomyRepository) Heartbeat(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
) error {
	return withTenantWritePoolTx(ctx, r.db, []string{tenantID}, func(dbTx tenantWriteTx) error {
		if _, err := dbTx.Exec(ctx, `
			UPDATE taxonomy_runs
			SET updated_at = NOW()
			WHERE id = $1 AND tenant_id = $2 AND status IN ('pending', 'running')`,
			runID, tenantID,
		); err != nil {
			return fmt.Errorf("heartbeat taxonomy run: %w", err)
		}

		return nil
	})
}

// FailRunIfStale marks a pending/running taxonomy run as failed only when its liveness timestamp is
// still older than cutoff at the moment of the update. The status and freshness checks are part of
// the tenant-locked UPDATE so a heartbeat that lands after candidate selection protects the run.
// Returns the database terminal timestamp when the run was failed and nil when it was refreshed,
// completed, or removed first.
func (r *TaxonomyRepository) FailRunIfStale(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
	cutoff time.Time,
	message string,
	errorCode models.TaxonomyRunFailureCode,
) (*time.Time, error) {
	var finishedAt *time.Time

	err := withTenantWritePoolTx(ctx, r.db, []string{tenantID}, func(dbTx tenantWriteTx) error {
		var terminalTime time.Time

		err := dbTx.QueryRow(ctx, `
			UPDATE taxonomy_runs
			SET status = 'failed', error = $3, error_code = $4, finished_at = NOW(), updated_at = NOW()
			WHERE id = $1
			  AND tenant_id = $2
			  AND status IN ('pending', 'running')
			  AND updated_at < $5
			RETURNING finished_at`,
			runID, tenantID, message, nullableFailureCode(errorCode), cutoff,
		).Scan(&terminalTime)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("fail stale taxonomy run: %w", err)
		}

		finishedAt = &terminalTime

		return nil
	})
	if err != nil {
		return nil, err
	}

	return finishedAt, nil
}

// ReapedTaxonomyRun identifies a run transitioned by the reaper for correlated operator logs.
type ReapedTaxonomyRun struct {
	ID         uuid.UUID
	TenantID   string
	ScopeType  models.TaxonomyScopeType
	SourceType string
	SourceID   string
	FieldID    string
	StartedAt  *time.Time
	CreatedAt  time.Time
	FinishedAt time.Time
}

// FailStuckRuns marks taxonomy runs stuck in a non-terminal state (pending/running) past olderThan
// as failed. Runs are orphaned when the taxonomy service crashes mid-run or its terminal callback is
// lost; without this sweep they are polled forever in the UI and block regeneration.
//
// Staleness is measured against updated_at, the run's liveness signal: the taxonomy service bumps it
// via Heartbeat while a generation is in flight (see Heartbeat), so olderThan is the maximum tolerated
// gap between heartbeats, not a ceiling on total run duration. Until heartbeats flow, updated_at holds
// the timestamp of the run's last state change, so the sweep degrades to a coarse "no progress since"
// safety net.
//
// Candidates are selected up front (a scoped update needs each run's tenant), then failed one at a
// time through FailRunIfStale so every mutation takes the shared tenant write lock — the repository
// invariant that coordinates tenant-owned writes with tenant-data purges. Stuck runs are rare, so a
// per-run loop rather than a batched per-tenant update is sufficient. The final UPDATE re-checks both
// status and updated_at under the tenant lock, so a run that heartbeats, reaches a terminal state, or
// is removed between selection and update is skipped. It returns the runs failed and the first
// unexpected error, if any.
func (r *TaxonomyRepository) FailStuckRuns(
	ctx context.Context,
	olderThan time.Duration,
	message string,
	errorCode models.TaxonomyRunFailureCode,
) ([]ReapedTaxonomyRun, error) {
	cutoff := time.Now().Add(-olderThan)

	type stuckRun struct {
		ReapedTaxonomyRun
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, tenant_id, scope_type, source_type, source_id, field_id, started_at, created_at
		FROM taxonomy_runs
		WHERE status IN ('pending', 'running')
		  AND updated_at < $1`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("select stuck taxonomy runs: %w", err)
	}

	var candidates []stuckRun

	for rows.Next() {
		var run stuckRun
		if scanErr := rows.Scan(
			&run.ID, &run.TenantID, &run.ScopeType, &run.SourceType, &run.SourceID, &run.FieldID,
			&run.StartedAt, &run.CreatedAt,
		); scanErr != nil {
			rows.Close()

			return nil, fmt.Errorf("scan stuck taxonomy run: %w", scanErr)
		}

		candidates = append(candidates, run)
	}

	rows.Close()

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate stuck taxonomy runs: %w", rowsErr)
	}

	var (
		reaped   []ReapedTaxonomyRun
		firstErr error
	)

	for _, run := range candidates {
		finishedAt, markErr := r.FailRunIfStale(ctx, run.ID, run.TenantID, cutoff, message, errorCode)
		if markErr == nil {
			if finishedAt != nil {
				run.FinishedAt = *finishedAt
				reaped = append(reaped, run.ReapedTaxonomyRun)
			}

			continue
		}

		// Skip a run when ErrTenantWriteConflict indicates that a tenant data purge holds the exclusive
		// lock, so the shared write lock is unavailable. The purge is deleting these runs anyway, and
		// any that survive it are caught by the next sweep.
		// Record the first genuinely unexpected error but keep reaping the remaining candidates.
		if errors.Is(markErr, huberrors.ErrTenantWriteConflict) {
			continue
		}

		if firstErr == nil {
			firstErr = markErr
		}
	}

	return reaped, firstErr
}

// GetRunForInternalService returns run metadata for internal taxonomy service-token workflows.
func (r *TaxonomyRepository) GetRunForInternalService(
	ctx context.Context,
	runID uuid.UUID,
) (*models.TaxonomyRun, error) {
	run, err := queryTaxonomyRun(ctx, r.db, taxonomyRunSelect+` FROM taxonomy_runs WHERE id = $1`, runID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("taxonomy_run", "taxonomy run not found")
		}

		return nil, fmt.Errorf("get taxonomy run: %w", err)
	}

	return run, nil
}

// GetRunForTenant returns a taxonomy run by ID scoped to a tenant.
func (r *TaxonomyRepository) GetRunForTenant(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
) (*models.TaxonomyRun, error) {
	run, err := queryTaxonomyRun(ctx, r.db, taxonomyRunSelect+`
		FROM taxonomy_runs
		WHERE id = $1 AND tenant_id = $2`,
		runID, tenantID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("taxonomy_run", "taxonomy run not found")
		}

		return nil, fmt.Errorf("get taxonomy run for tenant: %w", err)
	}

	return run, nil
}

// GetActiveRun returns the active taxonomy run for a scope.
func (r *TaxonomyRepository) GetActiveRun(ctx context.Context, scope models.TaxonomyScope) (*models.TaxonomyRun, error) {
	scopeType := taxonomyScopeType(scope)

	run, err := queryTaxonomyRun(ctx, r.db, taxonomyRunSelect+`
		FROM taxonomy_active_runs ar
		INNER JOIN taxonomy_runs ON taxonomy_runs.id = ar.run_id
		WHERE ar.tenant_id = $1
		  AND ar.scope_type = $2
		  AND ar.source_type = $3
		  AND ar.source_id = $4
		  AND ar.field_id = $5`,
		scope.TenantID, scopeType, scope.SourceType, scope.SourceID, scope.FieldID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("taxonomy_active_run", "active taxonomy run not found")
		}

		return nil, fmt.Errorf("get active taxonomy run: %w", err)
	}

	return run, nil
}

// ListRuns returns taxonomy run history for a tenant and optional scope filters.
func (r *TaxonomyRepository) ListRuns(
	ctx context.Context,
	filters models.ListTaxonomyRunsFilters,
) ([]models.TaxonomyRun, error) {
	limit := filters.Limit
	if limit <= 0 {
		limit = defaultTaxonomyRunsLimit
	}

	query := taxonomyRunSelect + ` FROM taxonomy_runs WHERE tenant_id = $1`
	args := []any{filters.TenantID}

	addFilter := func(column, value string) {
		if value == "" {
			return
		}

		args = append(args, value)
		query += fmt.Sprintf(" AND %s = $%d", column, len(args))
	}

	// addFilterPtr is the tri-state variant: nil skips the filter, while a non-nil
	// pointer filters by the exact stored value, including "" for the canonical
	// "no source" bucket (which addFilter would drop as an absent filter).
	addFilterPtr := func(column string, value *string) {
		if value == nil {
			return
		}

		args = append(args, *value)
		query += fmt.Sprintf(" AND %s = $%d", column, len(args))
	}

	if filters.ScopeType != "" {
		addFilter("scope_type", string(filters.ScopeType))
	}

	addFilter("source_type", filters.SourceType)
	addFilter("field_id", filters.FieldID)
	addFilterPtr("source_id", filters.SourceID)

	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", len(args))

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list taxonomy runs: %w", err)
	}
	defer rows.Close()

	return scanTaxonomyRuns(rows)
}

// MaxTaxonomyRunInputRows caps how many (record, embedding) rows one run-input fetch may
// materialize. run.RecordCount is simply "all eligible records in scope" with no upper
// bound, and each selected embedded row carries a 768-dim vector (~3 KB binary, ~8-10 KB as JSON) — a 200k-record
// tenant would otherwise allocate multiple GB in the API process for a single internal request.
// 10k rows ≈ 100 MB JSON keeps the endpoint safe while staying far above typical run sizes;
// larger scopes are truncated to the most recent rows (the ORDER BY) and logged.
const MaxTaxonomyRunInputRows = 10_000

// GetRunInput returns feedback records and embeddings for a taxonomy run, capped at
// MaxTaxonomyRunInputRows (most recent first).
func (r *TaxonomyRepository) GetRunInput(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
	embeddingModel string,
) (*models.TaxonomyRunInputResponse, error) {
	var response *models.TaxonomyRunInputResponse

	err := withTenantWritePoolTx(ctx, r.db, []string{tenantID}, func(dbTx tenantWriteTx) error {
		run, err := queryTaxonomyRun(ctx, dbTx, taxonomyRunSelect+`
			FROM taxonomy_runs
			WHERE id = $1 AND tenant_id = $2
			FOR UPDATE`, runID, tenantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return huberrors.NewNotFoundError("taxonomy_run", "taxonomy run not found")
		}

		if err != nil {
			return fmt.Errorf("lock taxonomy run for input materialization: %w", err)
		}

		limit := min(run.RecordCount, MaxTaxonomyRunInputRows)
		if run.RecordCount > MaxTaxonomyRunInputRows {
			slog.Warn("taxonomy run input truncated to the row cap",
				"run_id", runID, "eligible_count", run.RecordCount,
				"embedding_count", run.EmbeddingCount, "cap", MaxTaxonomyRunInputRows)
		}

		if err := materializeRunInputSnapshot(ctx, dbTx, run, limit, embeddingModel); err != nil {
			return err
		}

		var selectedCount int
		if err := dbTx.QueryRow(ctx, `
			SELECT COUNT(*)::int
			FROM taxonomy_run_input_records
			WHERE run_id = $1 AND tenant_id = $2`, runID, tenantID).Scan(&selectedCount); err != nil {
			return fmt.Errorf("count materialized taxonomy run input: %w", err)
		}

		rows, err := queryMaterializedRunInputRows(ctx, dbTx, runID, tenantID, embeddingModel)
		if err != nil {
			return err
		}
		defer rows.Close()

		records := make([]models.TaxonomyRunInputRecord, 0, selectedCount)

		for rows.Next() {
			var (
				record models.TaxonomyRunInputRecord
				vec    pgvector.HalfVector
			)
			if err := rows.Scan(
				&record.FeedbackRecordID,
				&record.SourceType,
				&record.SourceID,
				&record.FieldID,
				&record.FieldLabel,
				&record.ValueText,
				&vec,
			); err != nil {
				return fmt.Errorf("scan materialized taxonomy run input record: %w", err)
			}

			record.Embedding = vec.Slice()
			records = append(records, record)
		}

		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate materialized taxonomy run input records: %w", err)
		}

		if len(records) != selectedCount {
			return huberrors.NewConflictError(taxonomyRunInputChangedMessage)
		}

		run.EligibleCount = run.RecordCount
		run.SelectedCount = selectedCount
		run.SelectionCap = MaxTaxonomyRunInputRows
		run.SelectionTruncated = run.RecordCount > selectedCount
		run.SelectionStrategy = "most_recent"
		response = &models.TaxonomyRunInputResponse{Run: *run, Records: records}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("get taxonomy run input: %w", err)
	}

	return response, nil
}

// GetRunInputRecordIDs returns the exact bounded record selection a taxonomy result
// must cover. It intentionally omits feedback text and embeddings so completion does
// not materialize the full run input a second time.
func (r *TaxonomyRepository) GetRunInputRecordIDs(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT input.feedback_record_id, fr.id IS NOT NULL
		FROM taxonomy_run_input_records input
		LEFT JOIN feedback_records fr
			ON fr.id = input.feedback_record_id AND fr.tenant_id = input.tenant_id
		WHERE input.run_id = $1 AND input.tenant_id = $2
		ORDER BY input.sort_order`, runID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get taxonomy run input record IDs: %w", err)
	}
	defer rows.Close()

	recordIDs := make([]uuid.UUID, 0)

	for rows.Next() {
		var (
			recordID uuid.UUID
			readable bool
		)

		if err := rows.Scan(&recordID, &readable); err != nil {
			return nil, fmt.Errorf("scan taxonomy run input record ID: %w", err)
		}

		if !readable {
			return nil, huberrors.NewConflictError(taxonomyRunInputChangedMessage)
		}

		recordIDs = append(recordIDs, recordID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate taxonomy run input record IDs: %w", err)
	}

	return recordIDs, nil
}

// StoreResultAndActivate stores generated taxonomy artifacts and activates the run.
func (r *TaxonomyRepository) StoreResultAndActivate(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
	req models.TaxonomyRunResultRequest,
) (*models.TaxonomyRun, error) {
	var updated *models.TaxonomyRun

	err := withTenantWritePoolTx(ctx, r.db, []string{tenantID}, func(dbTx tenantWriteTx) error {
		var err error

		updated, err = storeResultAndActivateInTx(ctx, dbTx, runID, tenantID, req)

		return err
	})
	if err != nil {
		return nil, err
	}

	return updated, nil
}

func storeResultAndActivateInTx(
	ctx context.Context,
	transaction tenantWriteTx,
	runID uuid.UUID,
	tenantID string,
	req models.TaxonomyRunResultRequest,
) (*models.TaxonomyRun, error) {
	run, err := queryTaxonomyRun(ctx, transaction, taxonomyRunSelect+`
		FROM taxonomy_runs
		WHERE id = $1 AND tenant_id = $2
		FOR UPDATE`,
		runID, tenantID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("taxonomy_run", "taxonomy run not found")
		}

		return nil, fmt.Errorf("lock taxonomy run: %w", err)
	}

	if run.Status == models.TaxonomyRunStatusSucceeded && taxonomyResultDigestsMatch(run.Metrics, req.Metrics) {
		return run, nil
	}

	if run.Status != models.TaxonomyRunStatusRunning {
		return nil, taxonomyTransitionConflict(run, models.TaxonomyRunStatusSucceeded)
	}

	// Lock every still-live selected record before inserting memberships. The service's earlier
	// readability check is intentionally duplicated inside this write transaction: without these
	// key-share locks a record could be deleted between validation and the membership FK insert,
	// turning a permanent input change into a retryable 500.
	if run.InputSnapshotMaterialized {
		if err := lockReadableTaxonomyRunInput(ctx, transaction, run.ID, run.TenantID); err != nil {
			return nil, err
		}
	}

	clusterIDs, err := insertTaxonomyClustersBulk(ctx, transaction, run.ID, req.Clusters)
	if err != nil {
		return nil, err
	}

	if err := insertTaxonomyMembershipsBulk(ctx, transaction, run, req.Memberships, clusterIDs); err != nil {
		return nil, err
	}

	if err := insertTaxonomyNodesBulk(ctx, transaction, run.ID, req.Nodes, clusterIDs); err != nil {
		return nil, err
	}

	updated, err := queryTaxonomyRun(ctx, transaction, `
		WITH taxonomy_runs AS (
			UPDATE taxonomy_runs
			SET status = 'succeeded',
				metrics = $2,
				cluster_count = $3,
				node_count = $4,
				error = NULL,
				error_code = NULL,
				finished_at = NOW(),
				updated_at = NOW()
			WHERE id = $1
			RETURNING *
		)`+taxonomyRunSelect+` FROM taxonomy_runs`,
		run.ID,
		rawOrDefault(req.Metrics, defaultJSONObj),
		len(req.Clusters),
		len(req.Nodes),
	)
	if err != nil {
		return nil, fmt.Errorf("mark taxonomy run succeeded: %w", err)
	}

	activatedBy := taxonomyRunRequestedBy(run.Params)
	if _, err := transaction.Exec(ctx, `
			INSERT INTO taxonomy_active_runs (
				tenant_id, scope_type, source_type, source_id, field_id, run_id, activated_by
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (tenant_id, scope_type, source_type, source_id, field_id)
			DO UPDATE SET
				run_id = EXCLUDED.run_id,
				activated_by = EXCLUDED.activated_by,
				activated_at = NOW()`,
		run.TenantID,
		run.ScopeType,
		run.SourceType,
		run.SourceID,
		run.FieldID,
		run.ID,
		activatedBy,
	); err != nil {
		return nil, fmt.Errorf("activate taxonomy run: %w", err)
	}

	return updated, nil
}

type taxonomyClusterInsert struct {
	ID         uuid.UUID       `json:"id"`
	RunID      uuid.UUID       `json:"run_id"`
	ClusterKey int             `json:"cluster_key"`
	Label      *string         `json:"label"`
	LLMLabel   *string         `json:"llm_label"`
	Keywords   json.RawMessage `json:"keywords"`
	Size       int             `json:"size"`
	IsOutlier  bool            `json:"is_outlier"`
	Metrics    json.RawMessage `json:"metrics"`
}

func insertTaxonomyClustersBulk(
	ctx context.Context,
	transaction tenantWriteTx,
	runID uuid.UUID,
	clusters []models.TaxonomyResultCluster,
) (map[int]uuid.UUID, error) {
	clusterIDs := make(map[int]uuid.UUID, len(clusters))

	rows := make([]taxonomyClusterInsert, 0, len(clusters))
	for _, cluster := range clusters {
		if _, exists := clusterIDs[cluster.ClusterKey]; exists {
			return nil, huberrors.NewValidationError("clusters.cluster_key", "contains a duplicate cluster")
		}

		clusterID := uuid.New()
		clusterIDs[cluster.ClusterKey] = clusterID
		rows = append(rows, taxonomyClusterInsert{
			ID: clusterID, RunID: runID, ClusterKey: cluster.ClusterKey,
			Label: cluster.Label, LLMLabel: cluster.LLMLabel,
			Keywords: rawOrDefault(cluster.Keywords, defaultJSONArr),
			Size:     cluster.Size, IsOutlier: cluster.IsOutlier,
			Metrics: rawOrDefault(cluster.Metrics, defaultJSONObj),
		})
	}

	payload, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("marshal taxonomy clusters: %w", err)
	}

	if _, err := transaction.Exec(ctx, `
		INSERT INTO taxonomy_clusters (
			id, run_id, cluster_key, label, llm_label, keywords, size, is_outlier, metrics
		)
		SELECT id, run_id, cluster_key, label, llm_label, keywords, size, is_outlier, metrics
		FROM jsonb_to_recordset($1::jsonb) AS input(
			id uuid, run_id uuid, cluster_key integer, label text, llm_label text,
			keywords jsonb, size integer, is_outlier boolean, metrics jsonb
		)`, string(payload)); err != nil {
		return nil, fmt.Errorf("bulk insert taxonomy clusters: %w", err)
	}

	return clusterIDs, nil
}

type taxonomyMembershipInsert struct {
	RunID            uuid.UUID       `json:"run_id"`
	TenantID         string          `json:"tenant_id"`
	ClusterID        uuid.UUID       `json:"cluster_id"`
	FeedbackRecordID uuid.UUID       `json:"feedback_record_id"`
	Confidence       *float64        `json:"confidence"`
	Distance         *float64        `json:"distance"`
	Metadata         json.RawMessage `json:"metadata"`
}

func insertTaxonomyMembershipsBulk(
	ctx context.Context,
	transaction tenantWriteTx,
	run *models.TaxonomyRun,
	memberships []models.TaxonomyResultMembership,
	clusterIDs map[int]uuid.UUID,
) error {
	rows := make([]taxonomyMembershipInsert, 0, len(memberships))
	for _, membership := range memberships {
		clusterID, ok := clusterIDs[membership.ClusterKey]
		if !ok {
			return huberrors.NewValidationError("memberships.cluster_key", "references an unknown cluster")
		}

		rows = append(rows, taxonomyMembershipInsert{
			RunID: run.ID, TenantID: run.TenantID, ClusterID: clusterID,
			FeedbackRecordID: membership.FeedbackRecordID,
			Confidence:       membership.Confidence, Distance: membership.Distance,
			Metadata: rawOrDefault(membership.Metadata, defaultJSONObj),
		})
	}

	payload, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("marshal taxonomy memberships: %w", err)
	}

	if _, err := transaction.Exec(ctx, `
		INSERT INTO taxonomy_cluster_memberships (
			run_id, tenant_id, cluster_id, feedback_record_id, confidence, distance, metadata
		)
		SELECT run_id, tenant_id, cluster_id, feedback_record_id, confidence, distance, metadata
		FROM jsonb_to_recordset($1::jsonb) AS input(
			run_id uuid, tenant_id text, cluster_id uuid, feedback_record_id uuid,
			confidence double precision, distance double precision, metadata jsonb
		)`, string(payload)); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolationSQLState &&
			strings.Contains(pgErr.ConstraintName, "feedback_record_id") {
			return huberrors.NewConflictError(taxonomyRunInputChangedMessage)
		}

		return fmt.Errorf("bulk insert taxonomy cluster memberships: %w", err)
	}

	return nil
}

type taxonomyNodeInsert struct {
	ID            uuid.UUID       `json:"id"`
	RunID         uuid.UUID       `json:"run_id"`
	ParentID      *uuid.UUID      `json:"parent_id"`
	ClusterID     *uuid.UUID      `json:"cluster_id"`
	NodeType      string          `json:"node_type"`
	Label         string          `json:"label"`
	OriginalLabel string          `json:"original_label"`
	Description   *string         `json:"description"`
	Level         int             `json:"level"`
	SortOrder     int             `json:"sort_order"`
	Metadata      json.RawMessage `json:"metadata"`
}

func insertTaxonomyNodesBulk(
	ctx context.Context,
	transaction tenantWriteTx,
	runID uuid.UUID,
	nodes []models.TaxonomyResultNode,
	clusterIDs map[int]uuid.UUID,
) error {
	ordered := append([]models.TaxonomyResultNode(nil), nodes...)
	sort.SliceStable(ordered, func(leftIndex, rightIndex int) bool {
		if ordered[leftIndex].Level == ordered[rightIndex].Level {
			if ordered[leftIndex].SortOrder == ordered[rightIndex].SortOrder {
				return ordered[leftIndex].NodeKey < ordered[rightIndex].NodeKey
			}

			return ordered[leftIndex].SortOrder < ordered[rightIndex].SortOrder
		}

		return ordered[leftIndex].Level < ordered[rightIndex].Level
	})

	nodeIDs := make(map[string]uuid.UUID, len(ordered))

	rows := make([]taxonomyNodeInsert, 0, len(ordered))
	for _, node := range ordered {
		if _, exists := nodeIDs[node.NodeKey]; exists {
			return huberrors.NewValidationError("nodes.node_key", "contains a duplicate node")
		}

		var parentID *uuid.UUID

		if node.ParentKey != nil {
			resolved, ok := nodeIDs[*node.ParentKey]
			if !ok {
				return huberrors.NewValidationError("nodes.parent_key", "references an unknown parent")
			}

			parentID = &resolved
		}

		var clusterID *uuid.UUID

		if node.ClusterKey != nil {
			resolved, ok := clusterIDs[*node.ClusterKey]
			if !ok {
				return huberrors.NewValidationError("nodes.cluster_key", "references an unknown cluster")
			}

			clusterID = &resolved
		}

		nodeID := uuid.New()
		nodeIDs[node.NodeKey] = nodeID
		rows = append(rows, taxonomyNodeInsert{
			ID: nodeID, RunID: runID, ParentID: parentID, ClusterID: clusterID,
			NodeType: string(node.NodeType), Label: node.Label, OriginalLabel: node.Label,
			Description: node.Description, Level: node.Level, SortOrder: node.SortOrder,
			Metadata: rawOrDefault(node.Metadata, defaultJSONObj),
		})
	}

	payload, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("marshal taxonomy nodes: %w", err)
	}

	if _, err := transaction.Exec(ctx, `
		INSERT INTO taxonomy_nodes (
			id, run_id, parent_id, cluster_id, node_type, label, original_label,
			description, level, sort_order, metadata
		)
		SELECT id, run_id, parent_id, cluster_id, node_type, label, original_label,
			description, level, sort_order, metadata
		FROM jsonb_to_recordset($1::jsonb) AS input(
			id uuid, run_id uuid, parent_id uuid, cluster_id uuid, node_type taxonomy_node_type_enum,
			label text, original_label text, description text, level integer,
			sort_order integer, metadata jsonb
		)`, string(payload)); err != nil {
		return fmt.Errorf("bulk insert taxonomy nodes: %w", err)
	}

	return nil
}

func taxonomyResultDigestsMatch(existing, incoming json.RawMessage) bool {
	existingDigest := jsonutil.ObjectString(existing, "result_digest")

	return existingDigest != "" && existingDigest == jsonutil.ObjectString(incoming, "result_digest")
}

func taxonomyFailureCallbackMatches(
	run *models.TaxonomyRun,
	message string,
	errorCode models.TaxonomyRunFailureCode,
	metrics json.RawMessage,
) bool {
	if run.Error == nil || *run.Error != message || run.ErrorCode == nil || *run.ErrorCode != errorCode {
		return false
	}

	return jsonutil.CanonicalEqual(run.Metrics, metrics)
}

// GetTree returns the visible taxonomy tree for a run.
func (r *TaxonomyRepository) GetTree(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
) (*models.TaxonomyTreeResponse, error) {
	run, err := r.GetRunForTenant(ctx, runID, tenantID)
	if err != nil {
		return nil, err
	}

	nodes, err := r.listVisibleNodes(ctx, runID)
	if err != nil {
		return nil, err
	}

	root := buildTaxonomyTree(nodes)

	return &models.TaxonomyTreeResponse{Run: *run, Root: root}, nil
}

// CountNodeRecords returns the feedback-record count for every visible node in a taxonomy run.
// Each count is a subtree total: the number of DISTINCT feedback records assigned (through cluster
// membership) to the node or any of its visible descendants. So a branch reports the count across
// all of its subtopics and the root reports the run total. The run must belong to the tenant,
// otherwise a not-found error is returned.
//
// The count is derived from a single recursive descendant-closure query with COUNT(DISTINCT ...),
// which stays correct even if the same cluster is referenced by more than one node — a record is
// never double counted within a subtree. Records attach only through live cluster memberships, and
// a membership is removed by cascade when its feedback record is deleted, so counts track live data.
func (r *TaxonomyRepository) CountNodeRecords(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
) ([]models.TaxonomyNodeRecordCount, error) {
	if _, err := r.GetRunForTenant(ctx, runID, tenantID); err != nil {
		return nil, err
	}

	rows, err := r.db.Query(ctx, `
		WITH RECURSIVE visible_nodes AS (
			SELECT id, parent_id, cluster_id
			FROM taxonomy_nodes
			WHERE run_id = $1 AND removed_at IS NULL
		),
		subtree AS (
			-- Every visible node is an ancestor of itself, so it appears with a count of its own.
			SELECT id AS ancestor_id, id AS descendant_id, cluster_id
			FROM visible_nodes
			UNION ALL
			SELECT ancestor.ancestor_id, child.id, child.cluster_id
			FROM subtree ancestor
			INNER JOIN visible_nodes child ON child.parent_id = ancestor.descendant_id
		)
		SELECT subtree.ancestor_id, COUNT(DISTINCT tcm.feedback_record_id)
		FROM subtree
		LEFT JOIN taxonomy_cluster_memberships tcm
			ON tcm.run_id = $1
			AND tcm.tenant_id = $2
			AND tcm.cluster_id = subtree.cluster_id
		GROUP BY subtree.ancestor_id`,
		runID, tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("query taxonomy node record counts: %w", err)
	}
	defer rows.Close()

	counts := make([]models.TaxonomyNodeRecordCount, 0)

	for rows.Next() {
		var entry models.TaxonomyNodeRecordCount
		if err := rows.Scan(&entry.NodeID, &entry.RecordCount); err != nil {
			return nil, fmt.Errorf("scan taxonomy node record count: %w", err)
		}

		counts = append(counts, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate taxonomy node record counts: %w", err)
	}

	return counts, nil
}

// RenameNode updates a taxonomy node label and records an edit event.
func (r *TaxonomyRepository) RenameNode(
	ctx context.Context,
	nodeID uuid.UUID,
	tenantID string,
	actorID string,
	label string,
) (*models.TaxonomyNode, error) {
	var updated *models.TaxonomyNode

	err := withTenantWritePoolTx(ctx, r.db, []string{tenantID}, func(dbTx tenantWriteTx) error {
		node, run, err := getNodeForUpdate(ctx, dbTx, nodeID, tenantID)
		if err != nil {
			return err
		}

		updated, err = queryTaxonomyNode(ctx, dbTx, `
			WITH taxonomy_nodes AS (
				UPDATE taxonomy_nodes
				SET label = $2, updated_at = NOW()
				WHERE id = $1
				RETURNING *
			)`+taxonomyNodeSelect+` FROM taxonomy_nodes`,
			nodeID, label,
		)
		if err != nil {
			return fmt.Errorf("rename taxonomy node: %w", err)
		}

		return insertNodeEvent(ctx, dbTx, run, nodeID, "rename", actorID,
			map[string]string{"label": node.Label},
			map[string]string{"label": label})
	})
	if err != nil {
		return nil, err
	}

	return updated, nil
}

// RemoveNode soft-removes a taxonomy node and records an edit event.
func (r *TaxonomyRepository) RemoveNode(
	ctx context.Context,
	nodeID uuid.UUID,
	tenantID string,
	actorID string,
) (*models.TaxonomyNode, error) {
	var updated *models.TaxonomyNode

	err := withTenantWritePoolTx(ctx, r.db, []string{tenantID}, func(dbTx tenantWriteTx) error {
		_, run, err := getNodeForUpdate(ctx, dbTx, nodeID, tenantID)
		if err != nil {
			return err
		}

		updated, err = queryTaxonomyNode(ctx, dbTx, `
			WITH taxonomy_nodes AS (
				UPDATE taxonomy_nodes
				SET removed_at = NOW(), removed_by = $2, updated_at = NOW()
				WHERE id = $1
				RETURNING *
			)`+taxonomyNodeSelect+` FROM taxonomy_nodes`,
			nodeID, actorID,
		)
		if err != nil {
			return fmt.Errorf("remove taxonomy node: %w", err)
		}

		return insertNodeEvent(ctx, dbTx, run, nodeID, "soft_remove", actorID,
			map[string]any{"removed_at": nil},
			map[string]string{"removed_by": actorID})
	})
	if err != nil {
		return nil, err
	}

	return updated, nil
}

// ListNodeRecords returns feedback records assigned to a visible taxonomy node or descendants.
// The node must be visible and belong to the tenant, otherwise a not-found error is returned —
// the same contract as every other node-scoped operation.
func (r *TaxonomyRepository) ListNodeRecords(
	ctx context.Context,
	nodeID uuid.UUID,
	tenantID string,
	limit int,
) ([]models.FeedbackRecord, int, error) {
	if limit <= 0 {
		limit = defaultTaxonomyNodeRecordLimit
	}

	// The records query below is already tenant-safe on its own (it filters on the run's tenant, so a
	// foreign node simply matches no rows). The ownership guard is about the contract, not isolation:
	// without it a foreign or removed node is indistinguishable from one that genuinely holds no
	// records, which is what CountNodeRecords, GetTree, RenameNode and RemoveNode all avoid by
	// resolving ownership first.
	//
	// Both reads share one REPEATABLE READ snapshot. On separate statements a RemoveNode committing
	// between them would pass the guard and then match no rows in the recursive CTE, handing back the
	// ambiguous 200-empty this contract exists to remove. Under one snapshot the node is either
	// visible to both reads or to neither, so a removal mid-request lands as a 404. Read-only, so the
	// deferred rollback is the only exit.
	dbTx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, fmt.Errorf("begin taxonomy node records tx: %w", err)
	}

	defer rollbackQuietly(ctx, dbTx, "list taxonomy node records: rollback failed")

	if _, err := getNodeForTenant(ctx, dbTx, nodeID, tenantID); err != nil {
		return nil, 0, err
	}

	rows, err := dbTx.Query(ctx, `
		WITH RECURSIVE visible_nodes AS (
			SELECT id, run_id, cluster_id
			FROM taxonomy_nodes
			WHERE id = $1 AND removed_at IS NULL
			UNION ALL
			SELECT child.id, child.run_id, child.cluster_id
			FROM taxonomy_nodes child
			INNER JOIN visible_nodes parent ON parent.id = child.parent_id AND parent.run_id = child.run_id
			WHERE child.removed_at IS NULL
		)
		SELECT fr.id, fr.collected_at, fr.created_at, fr.updated_at,
			fr.source_type, fr.source_id, fr.source_name,
			fr.field_id, fr.field_label, fr.field_type, fr.field_group_id, fr.field_group_label,
			fr.value_text, fr.value_id, fr.value_number, fr.value_boolean, fr.value_date,
			fr.metadata, fr.language, fr.user_id, fr.tenant_id, fr.submission_id,
			fr.value_text_translated, fr.translation_lang_key,
			fr.sentiment, fr.sentiment_score,
			fr.emotions
		FROM visible_nodes vn
		INNER JOIN taxonomy_runs tr ON tr.id = vn.run_id
		INNER JOIN taxonomy_cluster_memberships tcm ON tcm.run_id = vn.run_id AND tcm.cluster_id = vn.cluster_id
		INNER JOIN feedback_records fr ON fr.id = tcm.feedback_record_id AND fr.tenant_id = tcm.tenant_id
		WHERE tr.tenant_id = $2
		ORDER BY fr.collected_at DESC, fr.id ASC
		LIMIT $3`,
		nodeID, tenantID, limit,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list taxonomy node records: %w", err)
	}
	defer rows.Close()

	records := []models.FeedbackRecord{}

	for rows.Next() {
		record, err := scanFeedbackRecord(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan taxonomy node record: %w", err)
		}

		records = append(records, *record)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate taxonomy node records: %w", err)
	}

	return records, limit, nil
}

func materializeRunInputSnapshot(
	ctx context.Context,
	dbTx tenantWriteTx,
	run *models.TaxonomyRun,
	limit int,
	embeddingModel string,
) error {
	var alreadyMaterialized bool
	if err := dbTx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM taxonomy_run_input_records WHERE run_id = $1
		)`, run.ID).Scan(&alreadyMaterialized); err != nil {
		return fmt.Errorf("check taxonomy run input snapshot: %w", err)
	}

	if alreadyMaterialized {
		return markRunInputSnapshotMaterialized(ctx, dbTx, run)
	}

	if run.ScopeType == models.TaxonomyScopeTypeDirectory {
		_, err := dbTx.Exec(ctx, `
			INSERT INTO taxonomy_run_input_records (
				run_id, tenant_id, feedback_record_id, sort_order
			)
			SELECT $1, $2, selected.id, selected.sort_order
			FROM (
				SELECT
					fr.id,
					(ROW_NUMBER() OVER (ORDER BY fr.collected_at DESC, fr.id ASC) - 1)::int AS sort_order
				FROM feedback_records fr
				INNER JOIN embeddings e ON e.feedback_record_id = fr.id AND e.model = $4
				WHERE fr.tenant_id = $2
				  AND COALESCE(
					NULLIF(btrim(fr.value_text_translated), ''),
					NULLIF(btrim(fr.value_text), '')
				  ) IS NOT NULL
				ORDER BY fr.collected_at DESC, fr.id ASC
				LIMIT $3
			) selected`,
			run.ID, run.TenantID, limit, embeddingModel)
		if err != nil {
			return fmt.Errorf("materialize directory taxonomy run input: %w", err)
		}

		return markRunInputSnapshotMaterialized(ctx, dbTx, run)
	}

	_, err := dbTx.Exec(ctx, `
		INSERT INTO taxonomy_run_input_records (
			run_id, tenant_id, feedback_record_id, sort_order
		)
		SELECT $1, $2, selected.id, selected.sort_order
		FROM (
			SELECT
				fr.id,
				(ROW_NUMBER() OVER (ORDER BY fr.collected_at DESC, fr.id ASC) - 1)::int AS sort_order
			FROM feedback_records fr
			INNER JOIN embeddings e ON e.feedback_record_id = fr.id AND e.model = $7
			WHERE fr.tenant_id = $2
			  AND fr.source_type = $3
			  AND NULLIF(btrim(fr.source_id), '') IS NOT DISTINCT FROM NULLIF(btrim($4), '')
			  AND fr.field_id = $5
			  AND COALESCE(
				NULLIF(btrim(fr.value_text_translated), ''),
				NULLIF(btrim(fr.value_text), '')
			  ) IS NOT NULL
			ORDER BY fr.collected_at DESC, fr.id ASC
			LIMIT $6
		) selected`,
		run.ID, run.TenantID, run.SourceType, run.SourceID, run.FieldID, limit, embeddingModel)
	if err != nil {
		return fmt.Errorf("materialize field taxonomy run input: %w", err)
	}

	return markRunInputSnapshotMaterialized(ctx, dbTx, run)
}

func markRunInputSnapshotMaterialized(ctx context.Context, dbTx tenantWriteTx, run *models.TaxonomyRun) error {
	if _, err := dbTx.Exec(ctx, `
		UPDATE taxonomy_runs
		SET input_snapshot_materialized = true
		WHERE id = $1 AND tenant_id = $2`, run.ID, run.TenantID); err != nil {
		return fmt.Errorf("mark taxonomy run input snapshot materialized: %w", err)
	}

	run.InputSnapshotMaterialized = true

	return nil
}

func lockReadableTaxonomyRunInput(
	ctx context.Context,
	transaction tenantWriteTx,
	runID uuid.UUID,
	tenantID string,
) error {
	rows, err := transaction.Query(ctx, `
		SELECT fr.id
		FROM taxonomy_run_input_records input
		INNER JOIN feedback_records fr
			ON fr.id = input.feedback_record_id AND fr.tenant_id = input.tenant_id
		WHERE input.run_id = $1 AND input.tenant_id = $2
		FOR KEY SHARE OF fr`, runID, tenantID)
	if err != nil {
		return fmt.Errorf("lock taxonomy run input records: %w", err)
	}

	liveCount := 0
	for rows.Next() {
		liveCount++
	}

	if err := rows.Err(); err != nil {
		rows.Close()

		return fmt.Errorf("iterate locked taxonomy run input records: %w", err)
	}

	rows.Close()

	var selectedCount int
	if err := transaction.QueryRow(ctx, `
		SELECT COUNT(*)::int
		FROM taxonomy_run_input_records
		WHERE run_id = $1 AND tenant_id = $2`, runID, tenantID).Scan(&selectedCount); err != nil {
		return fmt.Errorf("count locked taxonomy run input records: %w", err)
	}

	if liveCount != selectedCount {
		return huberrors.NewConflictError(taxonomyRunInputChangedMessage)
	}

	return nil
}

func queryMaterializedRunInputRows(
	ctx context.Context,
	dbTx tenantWriteTx,
	runID uuid.UUID,
	tenantID string,
	embeddingModel string,
) (pgx.Rows, error) {
	rows, err := dbTx.Query(ctx, `
		SELECT
			fr.id,
			fr.source_type,
			COALESCE(NULLIF(btrim(fr.source_id), ''), ''),
			fr.field_id,
			COALESCE(fr.field_label, ''),
			COALESCE(NULLIF(btrim(fr.value_text_translated), ''), NULLIF(btrim(fr.value_text), '')),
			e.embedding
		FROM taxonomy_run_input_records input
		INNER JOIN feedback_records fr
			ON fr.id = input.feedback_record_id AND fr.tenant_id = input.tenant_id
		INNER JOIN embeddings e
			ON e.feedback_record_id = input.feedback_record_id AND e.model = $3
		WHERE input.run_id = $1 AND input.tenant_id = $2
		ORDER BY input.sort_order`, runID, tenantID, embeddingModel)
	if err != nil {
		return nil, fmt.Errorf("query materialized taxonomy run input rows: %w", err)
	}

	return rows, nil
}

func (r *TaxonomyRepository) listVisibleNodes(ctx context.Context, runID uuid.UUID) ([]models.TaxonomyNode, error) {
	rows, err := r.db.Query(ctx, taxonomyNodeSelect+`
		FROM taxonomy_nodes
		WHERE run_id = $1 AND removed_at IS NULL
		ORDER BY level, sort_order, id`,
		runID,
	)
	if err != nil {
		return nil, fmt.Errorf("list taxonomy nodes: %w", err)
	}
	defer rows.Close()

	nodes := []models.TaxonomyNode{}

	for rows.Next() {
		node, err := scanTaxonomyNode(rows)
		if err != nil {
			return nil, err
		}

		nodes = append(nodes, *node)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate taxonomy nodes: %w", err)
	}

	return nodes, nil
}

// transitionError builds the conflict (or not-found) error for a run that could
// not be transitioned. It reads through the caller's query handle (q) — which is
// the open transaction for the in-tx callers — so it never checks out a second
// pooled connection while a transaction connection is held.
func (r *TaxonomyRepository) transitionError(
	ctx context.Context,
	q queryer,
	runID uuid.UUID,
	tenantID string,
	target models.TaxonomyRunStatus,
) error {
	run, err := queryTaxonomyRun(ctx, q, taxonomyRunSelect+`
		FROM taxonomy_runs
		WHERE id = $1 AND tenant_id = $2`,
		runID, tenantID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return huberrors.NewNotFoundError("taxonomy_run", "taxonomy run not found")
		}

		return fmt.Errorf("get taxonomy run: %w", err)
	}

	return taxonomyTransitionConflict(run, target)
}

func taxonomyTransitionConflict(run *models.TaxonomyRun, target models.TaxonomyRunStatus) error {
	return huberrors.NewConflictError(
		fmt.Sprintf("cannot transition taxonomy run from %s to %s", run.Status, target),
	)
}

func nullableFailureCode(code models.TaxonomyRunFailureCode) *string {
	if code == "" {
		return nil
	}

	value := string(code)

	return &value
}

type scanner interface {
	Scan(dest ...any) error
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const taxonomyRunSelect = `
			SELECT taxonomy_runs.id, taxonomy_runs.scope_type, taxonomy_runs.tenant_id, taxonomy_runs.source_type,
				taxonomy_runs.source_id, taxonomy_runs.field_id, taxonomy_runs.field_label,
				taxonomy_runs.status, taxonomy_runs.params, taxonomy_runs.metrics,
				taxonomy_runs.record_count, taxonomy_runs.embedding_count,
				taxonomy_runs.input_snapshot_materialized,
			taxonomy_runs.cluster_count, taxonomy_runs.node_count, taxonomy_runs.error, taxonomy_runs.error_code,
			taxonomy_runs.started_at, taxonomy_runs.finished_at,
			taxonomy_runs.created_at, taxonomy_runs.updated_at`

func queryTaxonomyRun(ctx context.Context, q queryer, sql string, args ...any) (*models.TaxonomyRun, error) {
	return scanTaxonomyRun(q.QueryRow(ctx, sql, args...))
}

func scanTaxonomyRun(row scanner) (*models.TaxonomyRun, error) {
	var (
		run       models.TaxonomyRun
		errorCode *string
	)

	if err := row.Scan(
		&run.ID,
		&run.ScopeType,
		&run.TenantID,
		&run.SourceType,
		&run.SourceID,
		&run.FieldID,
		&run.FieldLabel,
		&run.Status,
		&run.Params,
		&run.Metrics,
		&run.RecordCount,
		&run.EmbeddingCount,
		&run.InputSnapshotMaterialized,
		&run.ClusterCount,
		&run.NodeCount,
		&run.Error,
		&errorCode,
		&run.StartedAt,
		&run.FinishedAt,
		&run.CreatedAt,
		&run.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan taxonomy run: %w", err)
	}

	if errorCode != nil {
		code := models.TaxonomyRunFailureCode(*errorCode)
		run.ErrorCode = &code
	}

	return &run, nil
}

func scanTaxonomyRuns(rows pgx.Rows) ([]models.TaxonomyRun, error) {
	out := make([]models.TaxonomyRun, 0)

	for rows.Next() {
		run, err := scanTaxonomyRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan taxonomy run: %w", err)
		}

		out = append(out, *run)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate taxonomy runs: %w", err)
	}

	return out, nil
}

const taxonomyNodeSelect = `
		SELECT id, run_id, parent_id, cluster_id, node_type, label, original_label,
			description, level, sort_order, metadata, removed_at, removed_by, created_at, updated_at`

func queryTaxonomyNode(ctx context.Context, q queryer, sql string, args ...any) (*models.TaxonomyNode, error) {
	return scanTaxonomyNode(q.QueryRow(ctx, sql, args...))
}

func scanTaxonomyNode(row scanner) (*models.TaxonomyNode, error) {
	var node models.TaxonomyNode
	if err := row.Scan(
		&node.ID,
		&node.RunID,
		&node.ParentID,
		&node.ClusterID,
		&node.NodeType,
		&node.Label,
		&node.OriginalLabel,
		&node.Description,
		&node.Level,
		&node.SortOrder,
		&node.Metadata,
		&node.RemovedAt,
		&node.RemovedBy,
		&node.CreatedAt,
		&node.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan taxonomy node: %w", err)
	}

	return &node, nil
}

// A node is addressable by a tenant when it is visible and its run belongs to that tenant. Shared by
// the locking write path and the read path so the two can never drift apart on what "yours" means.
// $1 is the node id, $2 the tenant id.
const taxonomyNodeForTenantWhere = `
		WHERE id = $1 AND removed_at IS NULL
		  AND EXISTS (
		    SELECT 1 FROM taxonomy_runs
		    WHERE taxonomy_runs.id = taxonomy_nodes.run_id AND taxonomy_runs.tenant_id = $2
		  )`

// getNodeForTenant returns a visible taxonomy node addressable by the tenant, or a not-found error.
// The read-only counterpart of getNodeForUpdate: same ownership predicate, no row lock. It takes a
// queryer so the caller decides the snapshot — ListNodeRecords passes its transaction so the check
// and the records read cannot disagree about whether the node is still there.
func getNodeForTenant(
	ctx context.Context,
	q queryer,
	nodeID uuid.UUID,
	tenantID string,
) (*models.TaxonomyNode, error) {
	node, err := queryTaxonomyNode(ctx, q, taxonomyNodeSelect+`
		FROM taxonomy_nodes`+taxonomyNodeForTenantWhere,
		nodeID, tenantID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("taxonomy_node", "taxonomy node not found")
		}

		return nil, fmt.Errorf("get taxonomy node for tenant: %w", err)
	}

	return node, nil
}

// getNodeForUpdate takes a tenantWriteTx (not the narrower queryer) so the
// compiler enforces that the SELECT ... FOR UPDATE row lock is held for the
// life of a transaction; outside one, the lock would release at statement end.
func getNodeForUpdate(
	ctx context.Context,
	transaction tenantWriteTx,
	nodeID uuid.UUID,
	tenantID string,
) (*models.TaxonomyNode, *models.TaxonomyRun, error) {
	// The tenant predicate keeps the row lock tenant-scoped: a caller can never
	// lock another tenant's node row, even transiently.
	node, err := queryTaxonomyNode(ctx, transaction, taxonomyNodeSelect+`
		FROM taxonomy_nodes`+taxonomyNodeForTenantWhere+`
		FOR UPDATE`,
		nodeID, tenantID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, huberrors.NewNotFoundError("taxonomy_node", "taxonomy node not found")
		}

		return nil, nil, fmt.Errorf("lock taxonomy node: %w", err)
	}

	run, err := queryTaxonomyRun(ctx, transaction, taxonomyRunSelect+`
		FROM taxonomy_runs
		WHERE id = $1 AND tenant_id = $2`,
		node.RunID, tenantID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, huberrors.NewNotFoundError("taxonomy_node", "taxonomy node not found")
		}

		return nil, nil, fmt.Errorf("get taxonomy node run: %w", err)
	}

	return node, run, nil
}

func insertNodeEvent(
	ctx context.Context,
	transaction tenantWriteTx,
	run *models.TaxonomyRun,
	nodeID uuid.UUID,
	eventType string,
	actorID string,
	oldValue any,
	newValue any,
) error {
	oldJSON, err := json.Marshal(oldValue)
	if err != nil {
		return fmt.Errorf("marshal taxonomy node event old value: %w", err)
	}

	newJSON, err := json.Marshal(newValue)
	if err != nil {
		return fmt.Errorf("marshal taxonomy node event new value: %w", err)
	}

	if _, err := transaction.Exec(ctx, `
		INSERT INTO taxonomy_node_events (
			tenant_id, scope_type, source_type, source_id, field_id, run_id, node_id,
			event_type, actor_id, old_value, new_value
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		run.TenantID,
		run.ScopeType,
		run.SourceType,
		run.SourceID,
		run.FieldID,
		run.ID,
		nodeID,
		eventType,
		actorID,
		oldJSON,
		newJSON,
	); err != nil {
		return fmt.Errorf("insert taxonomy node event: %w", err)
	}

	return nil
}

func buildTaxonomyTree(nodes []models.TaxonomyNode) *models.TaxonomyNode {
	if len(nodes) == 0 {
		return nil
	}

	childrenByParent := make(map[uuid.UUID][]models.TaxonomyNode, len(nodes))

	var root *models.TaxonomyNode

	for _, node := range nodes {
		copyNode := node
		copyNode.Children = nil

		if copyNode.ParentID == nil {
			if root == nil {
				root = &copyNode
			}

			continue
		}

		childrenByParent[*copyNode.ParentID] = append(childrenByParent[*copyNode.ParentID], copyNode)
	}

	attachTaxonomyChildren(root, childrenByParent)

	return root
}

func attachTaxonomyChildren(node *models.TaxonomyNode, childrenByParent map[uuid.UUID][]models.TaxonomyNode) {
	if node == nil {
		return
	}

	node.Children = childrenByParent[node.ID]
	sort.SliceStable(node.Children, func(i, j int) bool {
		if node.Children[i].SortOrder == node.Children[j].SortOrder {
			return node.Children[i].ID.String() < node.Children[j].ID.String()
		}

		return node.Children[i].SortOrder < node.Children[j].SortOrder
	})

	for i := range node.Children {
		attachTaxonomyChildren(&node.Children[i], childrenByParent)
	}
}

func rawOrDefault(raw, fallback json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return fallback
	}

	return raw
}

func taxonomyRunRequestedBy(raw json.RawMessage) *string {
	var params struct {
		RequestedBy string `json:"requested_by"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &params) != nil || strings.TrimSpace(params.RequestedBy) == "" {
		return nil
	}

	return &params.RequestedBy
}
