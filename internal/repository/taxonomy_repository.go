package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/formbricks/hub/internal/huberrors"
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
			fr.source_id,
			COALESCE(MAX(fr.source_name) FILTER (WHERE fr.source_name IS NOT NULL AND btrim(fr.source_name) <> ''), ''),
			fr.field_id,
			COALESCE(MAX(fr.field_label) FILTER (WHERE fr.field_label IS NOT NULL AND btrim(fr.field_label) <> ''), ''),
			COUNT(*)::int,
			COUNT(e.feedback_record_id)::int
		FROM feedback_records fr
		LEFT JOIN embeddings e ON e.feedback_record_id = fr.id AND e.model = $2
		WHERE fr.tenant_id = $1
		  AND fr.source_id IS NOT NULL
		  AND btrim(fr.source_id) <> ''
		  AND fr.value_text IS NOT NULL
		  AND btrim(fr.value_text) <> ''
		GROUP BY fr.tenant_id, fr.source_type, fr.source_id, fr.field_id
		ORDER BY fr.source_type, fr.source_id, fr.field_id`,
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

	err := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*)::int,
			COUNT(e.feedback_record_id)::int,
			MAX(fr.field_label) FILTER (WHERE fr.field_label IS NOT NULL AND btrim(fr.field_label) <> '')
		FROM feedback_records fr
		LEFT JOIN embeddings e ON e.feedback_record_id = fr.id AND e.model = $5
		WHERE fr.tenant_id = $1
		  AND fr.source_type = $2
		  AND fr.source_id = $3
		  AND fr.field_id = $4
		  AND fr.value_text IS NOT NULL
		  AND btrim(fr.value_text) <> ''`,
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
	transaction, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("begin taxonomy run create tx: %w", err)
	}

	defer func() {
		_ = transaction.Rollback(ctx)
	}()

	lockKey := taxonomyScopeLockKey(params.TaxonomyScope)
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return nil, false, fmt.Errorf("lock taxonomy run scope: %w", err)
	}

	existing, err := queryTaxonomyRun(ctx, transaction, taxonomyRunSelect+`
		FROM taxonomy_runs
		WHERE tenant_id = $1
		  AND source_type = $2
		  AND source_id = $3
		  AND field_id = $4
		  AND status IN ('pending', 'running')
		ORDER BY created_at DESC, id DESC
		LIMIT 1`,
		params.TenantID, params.SourceType, params.SourceID, params.FieldID,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("find in-progress taxonomy run: %w", err)
	}

	if existing != nil {
		if err := transaction.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit existing taxonomy run tx: %w", err)
		}

		return existing, false, nil
	}

	run, err := queryTaxonomyRun(ctx, transaction, `
		WITH taxonomy_runs AS (
			INSERT INTO taxonomy_runs (
				tenant_id, source_type, source_id, field_id, field_label,
				status, params, record_count, embedding_count
			)
			VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7, $8)
			RETURNING *
		)`+taxonomyRunSelect+` FROM taxonomy_runs`,
		params.TenantID,
		params.SourceType,
		params.SourceID,
		params.FieldID,
		params.FieldLabel,
		rawOrDefault(params.Params, defaultJSONObj),
		params.RecordCount,
		params.EmbeddingCount,
	)
	if err != nil {
		return nil, false, fmt.Errorf("insert taxonomy run: %w", err)
	}

	if err := transaction.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit taxonomy run create tx: %w", err)
	}

	return run, true, nil
}

func taxonomyScopeLockKey(scope models.TaxonomyScope) string {
	return fmt.Sprintf(
		"%d:%s|%d:%s|%d:%s|%d:%s",
		len(scope.TenantID), scope.TenantID,
		len(scope.SourceType), scope.SourceType,
		len(scope.SourceID), scope.SourceID,
		len(scope.FieldID), scope.FieldID,
	)
}

// MarkRunRunning transitions a taxonomy run to running.
func (r *TaxonomyRepository) MarkRunRunning(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
) (*models.TaxonomyRun, error) {
	run, err := queryTaxonomyRun(ctx, r.db, `
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
			return nil, r.transitionError(ctx, runID, tenantID, models.TaxonomyRunStatusRunning)
		}

		return nil, fmt.Errorf("mark taxonomy run running: %w", err)
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
) (*models.TaxonomyRun, error) {
	run, err := queryTaxonomyRun(ctx, r.db, `
		WITH taxonomy_runs AS (
			UPDATE taxonomy_runs
			SET status = 'failed', error = $2, error_code = $3, finished_at = NOW(), updated_at = NOW()
			WHERE id = $1 AND tenant_id = $4 AND status IN ('pending', 'running')
			RETURNING *
		)`+taxonomyRunSelect+` FROM taxonomy_runs`,
		runID, message, nullableFailureCode(errorCode), tenantID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, r.transitionError(ctx, runID, tenantID, models.TaxonomyRunStatusFailed)
		}

		return nil, fmt.Errorf("mark taxonomy run failed: %w", err)
	}

	return run, nil
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
	run, err := queryTaxonomyRun(ctx, r.db, taxonomyRunSelect+`
		FROM taxonomy_active_runs ar
		INNER JOIN taxonomy_runs ON taxonomy_runs.id = ar.run_id
		WHERE ar.tenant_id = $1
		  AND ar.source_type = $2
		  AND ar.source_id = $3
		  AND ar.field_id = $4`,
		scope.TenantID, scope.SourceType, scope.SourceID, scope.FieldID,
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

	addFilter("source_type", filters.SourceType)
	addFilter("source_id", filters.SourceID)
	addFilter("field_id", filters.FieldID)

	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", len(args))

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list taxonomy runs: %w", err)
	}
	defer rows.Close()

	return scanTaxonomyRuns(rows)
}

// GetRunInput returns feedback records and embeddings for a taxonomy run.
func (r *TaxonomyRepository) GetRunInput(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
	embeddingModel string,
) (*models.TaxonomyRunInputResponse, error) {
	run, err := r.GetRunForTenant(ctx, runID, tenantID)
	if err != nil {
		return nil, err
	}

	rows, err := r.db.Query(ctx, `
		SELECT fr.id, COALESCE(fr.field_label, ''), fr.value_text, e.embedding
		FROM feedback_records fr
		INNER JOIN embeddings e ON e.feedback_record_id = fr.id AND e.model = $6
		WHERE fr.tenant_id = $1
		  AND fr.source_type = $2
		  AND fr.source_id = $3
		  AND fr.field_id = $4
		  AND fr.value_text IS NOT NULL
		  AND btrim(fr.value_text) <> ''
		ORDER BY fr.collected_at DESC, fr.id ASC
		LIMIT $5`,
		run.TenantID, run.SourceType, run.SourceID, run.FieldID, run.EmbeddingCount, embeddingModel,
	)
	if err != nil {
		return nil, fmt.Errorf("get taxonomy run input: %w", err)
	}
	defer rows.Close()

	records := make([]models.TaxonomyRunInputRecord, 0, run.EmbeddingCount)

	for rows.Next() {
		var (
			record models.TaxonomyRunInputRecord
			vec    pgvector.HalfVector
		)
		if err := rows.Scan(&record.FeedbackRecordID, &record.FieldLabel, &record.ValueText, &vec); err != nil {
			return nil, fmt.Errorf("scan taxonomy run input record: %w", err)
		}

		record.Embedding = vec.Slice()
		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate taxonomy run input records: %w", err)
	}

	return &models.TaxonomyRunInputResponse{Run: *run, Records: records}, nil
}

// StoreResultAndActivate stores generated taxonomy artifacts and activates the run.
func (r *TaxonomyRepository) StoreResultAndActivate(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
	req models.TaxonomyRunResultRequest,
) (*models.TaxonomyRun, error) {
	transaction, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin taxonomy result tx: %w", err)
	}

	defer func() {
		_ = transaction.Rollback(ctx)
	}()

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

	if run.Status != models.TaxonomyRunStatusRunning {
		return nil, taxonomyTransitionConflict(run, models.TaxonomyRunStatusSucceeded)
	}

	clusterIDs := make(map[int]uuid.UUID, len(req.Clusters))
	for _, cluster := range req.Clusters {
		var clusterID uuid.UUID
		if err := transaction.QueryRow(ctx, `
			INSERT INTO taxonomy_clusters (
				run_id, cluster_key, label, llm_label, keywords, size, is_outlier, metrics
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id`,
			run.ID,
			cluster.ClusterKey,
			cluster.Label,
			cluster.LLMLabel,
			rawOrDefault(cluster.Keywords, defaultJSONArr),
			cluster.Size,
			cluster.IsOutlier,
			rawOrDefault(cluster.Metrics, defaultJSONObj),
		).Scan(&clusterID); err != nil {
			return nil, fmt.Errorf("insert taxonomy cluster: %w", err)
		}

		clusterIDs[cluster.ClusterKey] = clusterID
	}

	for _, membership := range req.Memberships {
		clusterID, ok := clusterIDs[membership.ClusterKey]
		if !ok {
			return nil, huberrors.NewValidationError("memberships.cluster_key", "references an unknown cluster")
		}

		if _, err := transaction.Exec(ctx, `
			INSERT INTO taxonomy_cluster_memberships (
				run_id, tenant_id, cluster_id, feedback_record_id, confidence, distance, metadata
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			run.ID,
			run.TenantID,
			clusterID,
			membership.FeedbackRecordID,
			membership.Confidence,
			membership.Distance,
			rawOrDefault(membership.Metadata, defaultJSONObj),
		); err != nil {
			return nil, fmt.Errorf("insert taxonomy cluster membership: %w", err)
		}
	}

	nodes := append([]models.TaxonomyResultNode(nil), req.Nodes...)
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Level == nodes[j].Level {
			return nodes[i].SortOrder < nodes[j].SortOrder
		}

		return nodes[i].Level < nodes[j].Level
	})

	nodeIDs := make(map[string]uuid.UUID, len(nodes))
	for _, node := range nodes {
		var parentID *uuid.UUID

		if node.ParentKey != nil {
			resolved, ok := nodeIDs[*node.ParentKey]
			if !ok {
				return nil, huberrors.NewValidationError("nodes.parent_key", "references an unknown parent")
			}

			parentID = &resolved
		}

		var clusterID *uuid.UUID

		if node.ClusterKey != nil {
			resolved, ok := clusterIDs[*node.ClusterKey]
			if !ok {
				return nil, huberrors.NewValidationError("nodes.cluster_key", "references an unknown cluster")
			}

			clusterID = &resolved
		}

		var nodeID uuid.UUID
		if err := transaction.QueryRow(ctx, `
			INSERT INTO taxonomy_nodes (
				run_id, parent_id, cluster_id, node_type, label, original_label,
				description, level, sort_order, metadata
			)
			VALUES ($1, $2, $3, $4, $5, $5, $6, $7, $8, $9)
			RETURNING id`,
			run.ID,
			parentID,
			clusterID,
			node.NodeType,
			node.Label,
			node.Description,
			node.Level,
			node.SortOrder,
			rawOrDefault(node.Metadata, defaultJSONObj),
		).Scan(&nodeID); err != nil {
			return nil, fmt.Errorf("insert taxonomy node: %w", err)
		}

		nodeIDs[node.NodeKey] = nodeID
	}

	activatedBy := taxonomyRunRequestedBy(run.Params)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO taxonomy_active_runs (
			tenant_id, source_type, source_id, field_id, run_id, activated_by
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, source_type, source_id, field_id)
		DO UPDATE SET
			run_id = EXCLUDED.run_id,
			activated_by = EXCLUDED.activated_by,
			activated_at = NOW()`,
		run.TenantID,
		run.SourceType,
		run.SourceID,
		run.FieldID,
		run.ID,
		activatedBy,
	); err != nil {
		return nil, fmt.Errorf("activate taxonomy run: %w", err)
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

	if err := transaction.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit taxonomy result tx: %w", err)
	}

	return updated, nil
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

// RenameNode updates a taxonomy node label and records an edit event.
func (r *TaxonomyRepository) RenameNode(
	ctx context.Context,
	nodeID uuid.UUID,
	tenantID string,
	actorID string,
	label string,
) (*models.TaxonomyNode, error) {
	transaction, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin taxonomy node rename tx: %w", err)
	}

	defer func() {
		_ = transaction.Rollback(ctx)
	}()

	node, run, err := getNodeForUpdate(ctx, transaction, nodeID, tenantID)
	if err != nil {
		return nil, err
	}

	updated, err := queryTaxonomyNode(ctx, transaction, `
		WITH taxonomy_nodes AS (
			UPDATE taxonomy_nodes
			SET label = $2, updated_at = NOW()
			WHERE id = $1
			RETURNING *
		)`+taxonomyNodeSelect+` FROM taxonomy_nodes`,
		nodeID, label,
	)
	if err != nil {
		return nil, fmt.Errorf("rename taxonomy node: %w", err)
	}

	if err := insertNodeEvent(ctx, transaction, run, nodeID, "rename", actorID,
		map[string]string{"label": node.Label},
		map[string]string{"label": label}); err != nil {
		return nil, err
	}

	if err := transaction.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit taxonomy node rename tx: %w", err)
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
	transaction, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin taxonomy node remove tx: %w", err)
	}

	defer func() {
		_ = transaction.Rollback(ctx)
	}()

	_, run, err := getNodeForUpdate(ctx, transaction, nodeID, tenantID)
	if err != nil {
		return nil, err
	}

	updated, err := queryTaxonomyNode(ctx, transaction, `
		WITH taxonomy_nodes AS (
			UPDATE taxonomy_nodes
			SET removed_at = NOW(), removed_by = $2, updated_at = NOW()
			WHERE id = $1
			RETURNING *
		)`+taxonomyNodeSelect+` FROM taxonomy_nodes`,
		nodeID, actorID,
	)
	if err != nil {
		return nil, fmt.Errorf("remove taxonomy node: %w", err)
	}

	if err := insertNodeEvent(ctx, transaction, run, nodeID, "soft_remove", actorID,
		map[string]any{"removed_at": nil},
		map[string]string{"removed_by": actorID}); err != nil {
		return nil, err
	}

	if err := transaction.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit taxonomy node remove tx: %w", err)
	}

	return updated, nil
}

// ListNodeRecords returns feedback records assigned to a visible taxonomy node or descendants.
func (r *TaxonomyRepository) ListNodeRecords(
	ctx context.Context,
	nodeID uuid.UUID,
	tenantID string,
	limit int,
) ([]models.FeedbackRecord, int, error) {
	if limit <= 0 {
		limit = defaultTaxonomyNodeRecordLimit
	}

	rows, err := r.db.Query(ctx, `
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
			fr.value_text, fr.value_number, fr.value_boolean, fr.value_date,
			fr.metadata, fr.language, fr.user_id, fr.tenant_id, fr.submission_id
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

func (r *TaxonomyRepository) transitionError(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
	target models.TaxonomyRunStatus,
) error {
	run, err := r.GetRunForTenant(ctx, runID, tenantID)
	if err != nil {
		return err
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
		SELECT taxonomy_runs.id, taxonomy_runs.tenant_id, taxonomy_runs.source_type,
			taxonomy_runs.source_id, taxonomy_runs.field_id, taxonomy_runs.field_label,
			taxonomy_runs.status, taxonomy_runs.params, taxonomy_runs.metrics,
			taxonomy_runs.record_count, taxonomy_runs.embedding_count,
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

func getNodeForUpdate(
	ctx context.Context,
	transaction pgx.Tx,
	nodeID uuid.UUID,
	tenantID string,
) (*models.TaxonomyNode, *models.TaxonomyRun, error) {
	node, err := queryTaxonomyNode(ctx, transaction, taxonomyNodeSelect+`
		FROM taxonomy_nodes
		WHERE id = $1 AND removed_at IS NULL
		FOR UPDATE`,
		nodeID,
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
	transaction pgx.Tx,
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
			tenant_id, source_type, source_id, field_id, run_id, node_id,
			event_type, actor_id, old_value, new_value
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		run.TenantID,
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

func scanFeedbackRecord(row scanner) (*models.FeedbackRecord, error) {
	var record models.FeedbackRecord
	if err := row.Scan(
		&record.ID,
		&record.CollectedAt,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.SourceType,
		&record.SourceID,
		&record.SourceName,
		&record.FieldID,
		&record.FieldLabel,
		&record.FieldType,
		&record.FieldGroupID,
		&record.FieldGroupLabel,
		&record.ValueText,
		&record.ValueNumber,
		&record.ValueBoolean,
		&record.ValueDate,
		&record.Metadata,
		&record.Language,
		&record.UserID,
		&record.TenantID,
		&record.SubmissionID,
	); err != nil {
		return nil, fmt.Errorf("scan feedback record: %w", err)
	}

	return &record, nil
}
