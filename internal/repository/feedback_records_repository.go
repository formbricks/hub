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
//
// NOTE: the taxonomy node-records query (taxonomy_repository.go) carries a parallel,
// fr.-qualified copy of this list for its JOIN — keep that list, this const, and
// scanFeedbackRecord in sync. (Follow-up: derive the qualified list from this const.)
const feedbackRecordColumns = `id, collected_at, created_at, updated_at,
	source_type, source_id, source_name,
	field_id, field_label, field_type, field_group_id, field_group_label,
	value_text, value_number, value_boolean, value_date,
	metadata, language, user_id, tenant_id, submission_id,
	value_text_translated, translation_lang_key,
	sentiment, sentiment_score`

// scanFeedbackRecord materializes a FeedbackRecord from a row, in the exact column order of
// feedbackRecordColumns above. It lives beside that const so the SELECT/RETURNING order and
// the scan order can never drift. Shared with the taxonomy repository (same package).
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
		&record.ValueTextTranslated,
		&record.TranslationLangKey,
		&record.Sentiment,
		&record.SentimentScore,
	); err != nil {
		return nil, fmt.Errorf("scan feedback record: %w", err)
	}

	return &record, nil
}

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
//
// Setting a translation (translated != nil) is conditional: it lands only while langKey still
// equals the tenant's current EFFECTIVE target — its own target_language, or defaultLang
// (TRANSLATION_DEFAULT_LANGUAGE) when it has none — otherwise it returns
// huberrors.ErrTranslationSuperseded. This makes the write atomic w.r.t. a concurrent target
// change and immune to a stale settings-cache read, so an out-of-order stale-target job cannot
// clobber a newer translation. Clearing (translated == nil) is unconditional.
func (r *FeedbackRecordsRepository) SetTranslation(
	ctx context.Context, feedbackRecordID uuid.UUID, translated *string, langKey, defaultLang string,
) error {
	return withTenantWritePoolTx(ctx, r.db, nil, func(dbTx tenantWriteTx) error {
		// Translation rides the feedback record's tenant boundary; resolve and lock
		// it so the write cannot race a tenant data purge.
		if _, err := lockFeedbackRecordTenantShared(ctx, dbTx, feedbackRecordID); err != nil {
			return err
		}

		// Clearing (translated == nil) nulls both columns unconditionally: an emptied
		// value_text must drop any stale translation regardless of the tenant's target,
		// and a lang key has no meaning without a translation.
		if translated == nil {
			if _, err := dbTx.Exec(ctx, `
				UPDATE feedback_records
				SET value_text_translated = NULL, translation_lang_key = NULL, updated_at = NOW()
				WHERE id = $1`,
				feedbackRecordID,
			); err != nil {
				return fmt.Errorf("clear feedback record translation: %w", err)
			}

			return nil
		}

		// Setting a translation persists only while langKey still equals the tenant's current
		// EFFECTIVE target — COALESCE(its own target_language, defaultLang $4). This keeps the
		// write atomic against a concurrent target change: a stale-target job (an older target
		// finishing after the tenant switched — including switching to the default by clearing
		// its own target) resolves a different effective target, matches no row, and no-ops
		// instead of clobbering the current value. The LEFT JOIN keeps a tenant with no settings
		// row writable under the default; NULLIF($4,'') makes an empty default never match, so
		// with no default configured a tenant with no target of its own is not written.
		tag, err := dbTx.Exec(ctx, `
			UPDATE feedback_records fr
			SET value_text_translated = $2, translation_lang_key = $3, updated_at = NOW()
			FROM (
				SELECT NULLIF(ts.settings->>'target_language', '') AS stored_target
				FROM feedback_records r
				LEFT JOIN tenant_settings ts ON ts.tenant_id = r.tenant_id
				WHERE r.id = $1
			) eff
			WHERE fr.id = $1
				AND COALESCE(eff.stored_target, NULLIF($4, '')) = $3`,
			feedbackRecordID, *translated, langKey, defaultLang,
		)
		if err != nil {
			return fmt.Errorf("set feedback record translation: %w", err)
		}

		// The record exists (locked above), so zero rows means the tenant's effective target no
		// longer equals langKey (it changed its own target, or cleared it and the default
		// differs): a newer-target write owns the row. Signal a benign skip.
		if tag.RowsAffected() == 0 {
			return huberrors.ErrTranslationSuperseded
		}

		return nil
	})
}

// translationBackfillSelectSQL selects feedback records that need (re)translation: text
// fields with non-empty value_text whose EFFECTIVE target language differs from the stored
// translation_lang_key (never translated, or now stale). The effective target is the tenant's
// own target_language, falling back to $1 (the configured default) when the tenant has none;
// an empty $1 disables the fallback, so only tenants with their own target qualify. The LEFT
// JOIN keeps tenants with no settings row eligible under a non-empty default. Callers append
// ordering / keyset / limit clauses (params $2+).
const translationBackfillSelectSQL = `
	SELECT fr.id, COALESCE(NULLIF(ts.settings->>'target_language', ''), $1)
	FROM feedback_records fr
	LEFT JOIN tenant_settings ts ON ts.tenant_id = fr.tenant_id
	WHERE fr.field_type = 'text'
		AND fr.value_text IS NOT NULL AND btrim(fr.value_text) <> ''
		AND COALESCE(NULLIF(ts.settings->>'target_language', ''), $1) <> ''
		AND fr.translation_lang_key IS DISTINCT FROM COALESCE(NULLIF(ts.settings->>'target_language', ''), $1)`

// ListTranslationBackfillTargets returns one keyset page (fr.id > afterID, ordered by id, at
// most limit rows) of feedback records across all tenants that need (re)translation. Used by
// the one-off global backfill command. defaultLang is the fallback target for tenants with no
// target_language of their own ("" disables the fallback). Pass uuid.Nil as afterID for the
// first page.
func (r *FeedbackRecordsRepository) ListTranslationBackfillTargets(
	ctx context.Context, afterID uuid.UUID, limit int, defaultLang string,
) ([]models.TranslationBackfillTarget, error) {
	const query = translationBackfillSelectSQL + `
			AND fr.id > $2
		ORDER BY fr.id
		LIMIT $3`

	rows, err := r.db.Query(ctx, query, defaultLang, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("query translation backfill targets: %w", err)
	}

	return scanTranslationBackfillTargets(rows)
}

// ListTranslationBackfillTargetsForTenant returns one keyset page (fr.id > afterID, ordered
// by id, at most limit rows) of a single tenant's records that need (re)translation. The
// tenant filter + pagination let the settings-triggered backfill worker stream a large
// tenant without materializing every target at once. Pass uuid.Nil as afterID for the
// first page (every UUIDv7 id sorts after it).
func (r *FeedbackRecordsRepository) ListTranslationBackfillTargetsForTenant(
	ctx context.Context, tenantID string, afterID uuid.UUID, limit int, defaultLang string,
) ([]models.TranslationBackfillTarget, error) {
	const query = translationBackfillSelectSQL + `
			AND fr.tenant_id = $2
			AND fr.id > $3
		ORDER BY fr.id
		LIMIT $4`

	// defaultLang ($1) is the deployment fallback target: a tenant that cleared its own
	// target_language inherits it, so a settings-change backfill re-translates that tenant's
	// records to the default instead of no-opping. A tenant that set a new explicit target has
	// it stored, so the COALESCE in translationBackfillSelectSQL resolves to that target either way.
	rows, err := r.db.Query(ctx, query, defaultLang, tenantID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("query translation backfill targets for tenant: %w", err)
	}

	return scanTranslationBackfillTargets(rows)
}

// scanTranslationBackfillTargets collects (id, target_language) rows and closes rows.
func scanTranslationBackfillTargets(rows pgx.Rows) ([]models.TranslationBackfillTarget, error) {
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

	// Placeholders for value_text / language, captured so the stale-translation clear below can
	// compare the pre-update column to the new value (0 = the field is not in this update).
	valueTextArg, languageArg := 0, 0

	if req.ValueText != nil {
		valueTextArg = argCount
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
		languageArg = argCount
		updates = append(updates, fmt.Sprintf("language = $%d", argCount))
		args = append(args, *req.Language)
		argCount++
	}

	if req.UserID != nil {
		updates = append(updates, fmt.Sprintf("user_id = $%d", argCount))
		args = append(args, *req.UserID)
		argCount++
	}

	// Clear a now-stale translation, but only when value_text or language ACTUALLY changes:
	// the bare column on the RHS of an UPDATE ... SET is the pre-update value, so this compares
	// old vs new. Clearing only on a real change keeps "clear" aligned with a new content hash
	// — so the re-translation the change triggers is not deduped against the prior job and
	// actually repopulates — while a client re-sending an unchanged value leaves the valid
	// translation intact. NULLing on a real change also makes the row a backfill target
	// (translation_lang_key NULL IS DISTINCT FROM the tenant target), so a missed or
	// finally-failed re-translation is still recovered by a later backfill. Reuses the existing
	// value_text / language placeholders, so it consumes none of its own.
	var staleConds []string
	if valueTextArg != 0 {
		staleConds = append(staleConds, fmt.Sprintf("value_text IS DISTINCT FROM $%d", valueTextArg))
	}

	if languageArg != 0 {
		staleConds = append(staleConds, fmt.Sprintf("language IS DISTINCT FROM $%d", languageArg))
	}

	if len(staleConds) > 0 {
		cond := strings.Join(staleConds, " OR ")
		updates = append(updates,
			fmt.Sprintf("value_text_translated = CASE WHEN %s THEN NULL ELSE value_text_translated END", cond),
			fmt.Sprintf("translation_lang_key = CASE WHEN %s THEN NULL ELSE translation_lang_key END", cond),
		)
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
