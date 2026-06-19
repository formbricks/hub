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

// Upsert creates or replaces the tenant's settings and returns the stored row.
// Because the tenant_id is known up front, the write runs inside a transaction
// that first acquires the shared tenant write lock for that tenant, serializing
// it against a tenant data purge (a purge in progress yields a retryable
// *huberrors.TenantWriteConflictError before any write happens). The
// INSERT ... ON CONFLICT (tenant_id) only ever touches the row for this tenant,
// so a write can never affect another tenant's settings.
func (r *TenantSettingsRepository) Upsert(
	ctx context.Context, tenantID string, settings models.EnrichmentSettings,
) (*models.TenantSettings, error) {
	payload, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("marshal tenant settings: %w", err)
	}

	var result *models.TenantSettings

	err = withTenantWritePoolTx(ctx, r.db, []string{tenantID}, func(dbTx tenantWriteTx) error {
		// created_at/updated_at fall back to their column DEFAULT NOW() on insert;
		// only the conflict path needs to bump updated_at explicitly.
		const query = `
			INSERT INTO tenant_settings (tenant_id, settings)
			VALUES ($1, $2)
			ON CONFLICT (tenant_id) DO UPDATE
				SET settings = EXCLUDED.settings, updated_at = NOW()
			RETURNING tenant_id, settings`

		stored, scanErr := scanTenantSettings(dbTx.QueryRow(ctx, query, tenantID, json.RawMessage(payload)))
		if scanErr != nil {
			return fmt.Errorf("upsert tenant settings: %w", scanErr)
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
