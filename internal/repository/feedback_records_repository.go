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

// uniqueViolationSQLState is the SQLSTATE Postgres reports for unique
// constraint violations (23505 unique_violation).
const uniqueViolationSQLState = "23505"

// FeedbackRecordsRepository handles data access for feedback records.
type FeedbackRecordsRepository struct {
	db *pgxpool.Pool
}

// NewFeedbackRecordsRepository creates a new feedback records repository.
func NewFeedbackRecordsRepository(db *pgxpool.Pool) *FeedbackRecordsRepository {
	return &FeedbackRecordsRepository{db: db}
}

// feedbackRecordColumns is the canonical SELECT/RETURNING column list for a
// FeedbackRecord, in the exact order scanFeedbackRecord reads it. Together they are
// the single source of truth for materializing a FeedbackRecord, so column order
// and scan order cannot drift across the Create/Get/List/Update read paths (a
// silent runtime scan error otherwise). It excludes derived rows like embeddings.
const feedbackRecordColumns = `id, collected_at, created_at, updated_at,
	source_type, source_id, source_name,
	field_id, field_label, field_type, field_group_id, field_group_label,
	value_text, value_number, value_boolean, value_date,
	metadata, language, user_id, tenant_id, submission_id,
	value_text_translated, translation_lang_key`

// Create inserts a new feedback record.
func (r *FeedbackRecordsRepository) Create(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	collectedAt := time.Now()
	if req.CollectedAt != nil {
		collectedAt = *req.CollectedAt
	}

	// The tenant is known up front, so gate the insert on the shared tenant
	// write lock in a single statement (held for this statement's implicit
	// transaction): one round trip, same isolation against a tenant data purge.
	// Zero rows means the lock was refused (purge in progress).
	const lockKeyParam = 19 // $19, after the 18 inserted columns

	query := `
		INSERT INTO feedback_records (
			collected_at, source_type, source_id, source_name,
			field_id, field_label, field_type, field_group_id, field_group_label,
			value_text, value_number, value_boolean, value_date,
			metadata, language, user_id, tenant_id, submission_id
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
		WHERE ` + tenantWriteLockGate(lockKeyParam) + `
		RETURNING ` + feedbackRecordColumns

	record, err := scanFeedbackRecord(r.db.QueryRow(ctx, query,
		collectedAt, req.SourceType, req.SourceID, req.SourceName,
		req.FieldID, req.FieldLabel, req.FieldType, req.FieldGroupID, req.FieldGroupLabel,
		req.ValueText, req.ValueNumber, req.ValueBoolean, req.ValueDate,
		req.Metadata, req.Language, req.UserID, req.TenantID, req.SubmissionID,
		TenantWriteLockKey(req.TenantID),
	))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationSQLState {
			return nil, huberrors.NewConflictError("a feedback record with this tenant_id, submission_id, and field_id already exists")
		}

		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewTenantWriteConflictError("tenant data purge in progress for this tenant; retry later")
		}

		return nil, fmt.Errorf("failed to create feedback record: %w", err)
	}

	return record, nil
}

// resolveFeedbackRecordTenant reads the tenant boundary of a feedback record
// inside the current transaction so the caller can acquire the tenant write
// lock before mutating. tenant_id is immutable on feedback records, so a
// plain read is race-free once the lock is held.
func resolveFeedbackRecordTenant(ctx context.Context, querier queryer, id uuid.UUID) (string, error) {
	var tenantID string

	err := querier.QueryRow(ctx, `SELECT tenant_id FROM feedback_records WHERE id = $1`, id).Scan(&tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", huberrors.NewNotFoundError("feedback record", "feedback record not found")
		}

		return "", fmt.Errorf("resolve feedback record tenant: %w", err)
	}

	return tenantID, nil
}

// lockFeedbackRecordTenantShared resolves a feedback record's tenant boundary
// and acquires the shared tenant write lock for it, in that order, inside the
// current transaction. It is the single entry point for mutating a feedback
// record (or its derived rows, e.g. embeddings) by ID without racing a tenant
// data purge. Returns NotFound if the record is gone and a tenant write
// conflict if the tenant is under purge.
func lockFeedbackRecordTenantShared(ctx context.Context, dbTx tenantWriteTx, id uuid.UUID) (string, error) {
	tenantID, err := resolveFeedbackRecordTenant(ctx, dbTx, id)
	if err != nil {
		return "", err
	}

	if err := tryLockTenantsShared(ctx, dbTx, []string{tenantID}); err != nil {
		return "", err
	}

	return tenantID, nil
}

// GetByID retrieves a single feedback record by ID. Embedding is not selected (API/worker reads stay lean).
func (r *FeedbackRecordsRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error) {
	query := `SELECT ` + feedbackRecordColumns + `
		FROM feedback_records
		WHERE id = $1`

	record, err := scanFeedbackRecord(r.db.QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("feedback record", "feedback record not found")
		}

		return nil, fmt.Errorf("failed to get feedback record: %w", err)
	}

	return record, nil
}

// SetTranslation stores the translated text and the target locale it was produced in
// for a feedback record. The write is scoped to the record's tenant via the shared
// tenant write lock (so it cannot race a tenant data purge) and does NOT publish a
// domain event: translation is a derived enrichment, not a record edit, and
// re-publishing would loop the enrichment pipeline. A missing record (deleted or
// purged between read and write) returns NotFound. translated may be nil.
func (r *FeedbackRecordsRepository) SetTranslation(
	ctx context.Context, feedbackRecordID uuid.UUID, translated *string, langKey string,
) error {
	return withTenantWritePoolTx(ctx, r.db, nil, func(dbTx tenantWriteTx) error {
		// Translation rides the feedback record's tenant boundary; resolve and lock
		// it so the write cannot race a tenant data purge.
		if _, err := lockFeedbackRecordTenantShared(ctx, dbTx, feedbackRecordID); err != nil {
			return err
		}

		// Clearing (translated == nil) nulls both columns: a lang key has no meaning
		// without a translation.
		var langKeyArg any
		if translated != nil {
			langKeyArg = langKey
		}

		_, err := dbTx.Exec(ctx, `
			UPDATE feedback_records
			SET value_text_translated = $2, translation_lang_key = $3, updated_at = NOW()
			WHERE id = $1`,
			feedbackRecordID, translated, langKeyArg,
		)
		if err != nil {
			return fmt.Errorf("set feedback record translation: %w", err)
		}

		return nil
	})
}

// ListTranslationBackfillTargets returns feedback records that need (re)translation:
// text fields with non-empty value_text whose tenant has a target language configured
// and whose stored translation_lang_key differs from that target (never translated, or
// translated to a now-stale target). It joins tenant_settings for the per-tenant target.
func (r *FeedbackRecordsRepository) ListTranslationBackfillTargets(
	ctx context.Context,
) ([]models.TranslationBackfillTarget, error) {
	const query = `
		SELECT fr.id, ts.settings->>'target_language'
		FROM feedback_records fr
		JOIN tenant_settings ts ON ts.tenant_id = fr.tenant_id
		WHERE fr.field_type = 'text'
			AND fr.value_text IS NOT NULL AND btrim(fr.value_text) <> ''
			AND COALESCE(ts.settings->>'target_language', '') <> ''
			AND fr.translation_lang_key IS DISTINCT FROM ts.settings->>'target_language'
		ORDER BY fr.id`

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query translation backfill targets: %w", err)
	}
	defer rows.Close()

	var targets []models.TranslationBackfillTarget

	for rows.Next() {
		var target models.TranslationBackfillTarget
		if err := rows.Scan(&target.FeedbackRecordID, &target.TargetLang); err != nil {
			return nil, fmt.Errorf("scan translation backfill target: %w", err)
		}

		targets = append(targets, target)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate translation backfill targets: %w", err)
	}

	return targets, nil
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

const feedbackRecordsListSelect = `SELECT ` + feedbackRecordColumns + `
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

	// tenant_id is appended by the caller after resolving the row's tenant
	// boundary inside the tenant write transaction.
	query = fmt.Sprintf(`
		UPDATE feedback_records
		SET %s
		WHERE id = $%d AND tenant_id = $%d
		RETURNING `+feedbackRecordColumns,
		strings.Join(updates, ", "), argCount, argCount+1)

	return query, args, true
}

// Update updates an existing feedback record
// Only value fields, metadata, language, and user_id can be updated.
func (r *FeedbackRecordsRepository) Update(
	ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	query, args, hasUpdates := buildUpdateQuery(req, id, time.Now())
	if !hasUpdates {
		// No write happens, so no tenant write lock is needed.
		return r.GetByID(ctx, id)
	}

	var record *models.FeedbackRecord

	err := withTenantWritePoolTx(ctx, r.db, nil, func(dbTx tenantWriteTx) error {
		tenantID, err := lockFeedbackRecordTenantShared(ctx, dbTx, id)
		if err != nil {
			return err
		}

		scanned, err := scanFeedbackRecord(dbTx.QueryRow(ctx, query, append(args, tenantID)...))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return huberrors.NewNotFoundError("feedback record", "feedback record not found")
			}

			return fmt.Errorf("failed to update feedback record: %w", err)
		}

		record = scanned

		return nil
	})
	if err != nil {
		return nil, err
	}

	return record, nil
}

// Delete removes a feedback record.
func (r *FeedbackRecordsRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return withTenantWritePoolTx(ctx, r.db, nil, func(dbTx tenantWriteTx) error {
		tenantID, err := lockFeedbackRecordTenantShared(ctx, dbTx, id)
		if err != nil {
			return err
		}

		result, err := dbTx.Exec(ctx, `DELETE FROM feedback_records WHERE id = $1 AND tenant_id = $2`, id, tenantID)
		if err != nil {
			return fmt.Errorf("failed to delete feedback record: %w", err)
		}

		if result.RowsAffected() == 0 {
			return huberrors.NewNotFoundError("feedback record", "feedback record not found")
		}

		return nil
	})
}

// DeleteByUser deletes all feedback records matching user_id.
// When tenant_id is provided, deletion is restricted to that tenant; otherwise all user records
// are deleted across tenants (documented GDPR/right-to-erasure exception).
// Every spanned tenant's write lock is acquired before deleting; if any tenant is under purge
// the whole request fails with a retryable conflict. The delete is scoped to the locked tenants,
// so records appearing in new tenants mid-transaction are never touched without a lock.
// Because the tenant set is snapshotted before locking, a record could be written into a new
// (unlocked) tenant for the same user after the snapshot; after deleting, the same transaction
// re-checks for any in-scope record still present and, if found, returns a retryable conflict
// (rolling the whole delete back) rather than reporting an incomplete erasure as success. Erasure
// is idempotent, so the caller's retry converges once writes for the subject have stopped.
// It returns deleted IDs grouped by tenant so callers can publish tenant-scoped side effects.
func (r *FeedbackRecordsRepository) DeleteByUser(
	ctx context.Context, filters *models.DeleteFeedbackRecordsByUserFilters,
) ([]models.DeletedFeedbackRecordsByTenant, error) {
	groups := make([]models.DeletedFeedbackRecordsByTenant, 0)

	err := withTenantWritePoolTx(ctx, r.db, nil, func(dbTx tenantWriteTx) error {
		tenantIDs, err := listUserFeedbackTenants(ctx, dbTx, filters)
		if err != nil {
			return err
		}

		if len(tenantIDs) == 0 {
			// Nothing to delete; keep the endpoint idempotent without taking locks.
			return nil
		}

		if err := tryLockTenantsShared(ctx, dbTx, tenantIDs); err != nil {
			return err
		}

		rows, err := dbTx.Query(ctx, `
			DELETE FROM feedback_records
			WHERE user_id = $1 AND tenant_id = ANY($2)
			RETURNING id, tenant_id`, filters.UserID, tenantIDs)
		if err != nil {
			return fmt.Errorf("failed to delete feedback records by user: %w", err)
		}
		defer rows.Close()

		groupIndexByTenant := make(map[string]int)

		for rows.Next() {
			var (
				id       uuid.UUID
				tenantID string
			)

			if err := rows.Scan(&id, &tenantID); err != nil {
				return fmt.Errorf("failed to scan deleted feedback record id: %w", err)
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
			return fmt.Errorf("error iterating delete feedback records by user result: %w", err)
		}

		// Drift guard: a record for this user may have been written into a tenant
		// not in the locked snapshot (a new tenant for the all-tenant erase, or a
		// concurrent insert into an already-locked tenant). Re-check the in-scope
		// set; if anything survives, fail with a retryable conflict so the whole
		// erase rolls back and the caller retries, rather than reporting a
		// partial erasure as complete.
		if err := ensureNoResidualUserFeedback(ctx, dbTx, filters); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return groups, nil
}

// ensureNoResidualUserFeedback returns a retryable tenant write conflict if any
// in-scope feedback record for the user still exists after DeleteByUser's delete.
func ensureNoResidualUserFeedback(
	ctx context.Context, dbTx tenantWriteTx, filters *models.DeleteFeedbackRecordsByUserFilters,
) error {
	query := `SELECT EXISTS (SELECT 1 FROM feedback_records WHERE user_id = $1`
	args := []any{filters.UserID}

	if filters.TenantID != nil {
		query += ` AND tenant_id = $2`

		args = append(args, *filters.TenantID)
	}

	query += `)`

	var remaining bool
	if err := dbTx.QueryRow(ctx, query, args...).Scan(&remaining); err != nil {
		return fmt.Errorf("check residual feedback records for user: %w", err)
	}

	if remaining {
		return huberrors.NewTenantWriteConflictError("feedback records for this user changed during deletion; retry")
	}

	return nil
}

// listUserFeedbackTenants returns the distinct tenant boundaries holding feedback
// records for the user (optionally restricted to one tenant), so DeleteByUser can
// lock each one before deleting.
func listUserFeedbackTenants(
	ctx context.Context, dbTx tenantWriteTx, filters *models.DeleteFeedbackRecordsByUserFilters,
) ([]string, error) {
	query := `SELECT DISTINCT tenant_id FROM feedback_records WHERE user_id = $1`
	args := []any{filters.UserID}

	if filters.TenantID != nil {
		query += ` AND tenant_id = $2`

		args = append(args, *filters.TenantID)
	}

	query += ` ORDER BY tenant_id`

	rows, err := dbTx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list tenants for user feedback records: %w", err)
	}
	defer rows.Close()

	var tenantIDs []string

	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return nil, fmt.Errorf("failed to scan tenant id: %w", err)
		}

		tenantIDs = append(tenantIDs, tenantID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tenants for user feedback records: %w", err)
	}

	return tenantIDs, nil
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
		record, err := scanFeedbackRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan feedback record: %w", err)
		}

		records = append(records, *record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating feedback records: %w", err)
	}

	return records, nil
}
