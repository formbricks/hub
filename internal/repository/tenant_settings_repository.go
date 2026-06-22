package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/formbricks/hub/internal/models"
)

// TenantSettingsRepository handles data access for the tenant_settings table.
type TenantSettingsRepository struct {
	db *pgxpool.Pool
}

// NewTenantSettingsRepository creates a new tenant settings repository.
func NewTenantSettingsRepository(db *pgxpool.Pool) *TenantSettingsRepository {
	return &TenantSettingsRepository{db: db}
}

// Get returns the tenant's settings row and found=true, or (nil, false, nil)
// when the tenant has none. The query is always scoped to the given tenant_id,
// so one tenant's settings can never be read under another tenant.
func (r *TenantSettingsRepository) Get(
	ctx context.Context, tenantID string,
) (*models.TenantSettings, bool, error) {
	const query = `
		SELECT tenant_id, settings
		FROM tenant_settings
		WHERE tenant_id = $1`

	settings, err := scanTenantSettings(r.db.QueryRow(ctx, query, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("get tenant settings: %w", err)
	}

	return settings, true, nil
}

// Upsert creates or replaces (full replace) the tenant's settings and returns the
// stored row.
func (r *TenantSettingsRepository) Upsert(
	ctx context.Context, tenantID string, settings models.EnrichmentSettings,
) (*models.TenantSettings, error) {
	payload, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("marshal tenant settings: %w", err)
	}

	// created_at/updated_at fall back to their column DEFAULT NOW() on insert; only
	// the conflict path bumps updated_at. EXCLUDED.settings replaces the whole object.
	const query = `
		INSERT INTO tenant_settings (tenant_id, settings)
		VALUES ($1, $2)
		ON CONFLICT (tenant_id) DO UPDATE
			SET settings = EXCLUDED.settings, updated_at = NOW()
		RETURNING tenant_id, settings`

	return r.writeSettings(ctx, tenantID, query, payload)
}

// Patch applies an RFC 7396 JSON Merge Patch to the tenant's settings: keys in
// set are written (a top-level JSONB `||`), keys in removeKeys are deleted
// (`- text[]`), and keys mentioned in neither are left untouched. For a tenant
// with no row yet the set object becomes the initial settings. set and removeKeys
// are disjoint, so the merge-then-remove order does not matter.
func (r *TenantSettingsRepository) Patch(
	ctx context.Context, tenantID string, set models.EnrichmentSettings, removeKeys []string,
) (*models.TenantSettings, error) {
	payload, err := json.Marshal(set)
	if err != nil {
		return nil, fmt.Errorf("marshal tenant settings patch: %w", err)
	}

	// A nil slice binds as SQL NULL and `jsonb - NULL` is NULL (which would blank
	// the column); an empty, non-nil slice binds as an empty text[] and
	// `jsonb - '{}'` is a no-op. So never pass nil to the `- $3` removal.
	if removeKeys == nil {
		removeKeys = []string{}
	}

	// `||` merges the provided keys (settings the patch does not mention survive);
	// `- $3` then deletes the keys the merge patch set to null.
	const query = `
		INSERT INTO tenant_settings (tenant_id, settings)
		VALUES ($1, $2)
		ON CONFLICT (tenant_id) DO UPDATE
			SET settings = (tenant_settings.settings || EXCLUDED.settings) - $3::text[], updated_at = NOW()
		RETURNING tenant_id, settings`

	return r.writeSettings(ctx, tenantID, query, payload, removeKeys)
}

// writeSettings runs a settings write (an INSERT ... ON CONFLICT ... RETURNING with
// $1 = tenant_id, $2 = the settings JSONB, and any extraArgs as $3, $4, …) inside
// the shared tenant write lock for that tenant. The lock serializes the write
// against a tenant data purge (a purge in progress yields a retryable
// *huberrors.TenantWriteConflictError before any write happens), and the
// tenant-scoped statement only ever touches this tenant's row, so a write can
// never affect another tenant's settings.
func (r *TenantSettingsRepository) writeSettings(
	ctx context.Context, tenantID, query string, payload []byte, extraArgs ...any,
) (*models.TenantSettings, error) {
	var result *models.TenantSettings

	// $1 = tenant_id, $2 = settings JSONB; any extraArgs follow as $3, $4, …
	args := append([]any{tenantID, json.RawMessage(payload)}, extraArgs...)

	err := withTenantWritePoolTx(ctx, r.db, []string{tenantID}, func(dbTx tenantWriteTx) error {
		stored, scanErr := scanTenantSettings(dbTx.QueryRow(ctx, query, args...))
		if scanErr != nil {
			return fmt.Errorf("write tenant settings: %w", scanErr)
		}

		result = stored

		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// scanTenantSettings maps a (tenant_id, settings) row into the typed model,
// decoding the JSONB settings column into EnrichmentSettings. The raw Scan error
// (including pgx.ErrNoRows) is returned unwrapped so callers can match on it;
// callers add operation context.
func scanTenantSettings(row pgx.Row) (*models.TenantSettings, error) {
	var (
		settings models.TenantSettings
		raw      []byte
	)

	if err := row.Scan(&settings.TenantID, &raw); err != nil {
		return nil, err //nolint:wrapcheck // raw error preserved for pgx.ErrNoRows detection; callers wrap
	}

	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings.Settings); err != nil {
			return nil, fmt.Errorf("decode tenant settings json: %w", err)
		}
	}

	return &settings, nil
}
