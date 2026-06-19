-- +goose Up
-- Tenant-scoped settings for Hub enrichment configuration. One row per tenant,
-- keyed by the natural tenant_id (there is no tenants table, so it is an
-- unconstrained VARCHAR like every other tenant-owned table). The open-ended
-- `settings` JSONB holds typed settings (today: target_language), so new settings
-- are added as struct fields without a schema migration.
CREATE TABLE tenant_settings (
  tenant_id  VARCHAR(255) PRIMARY KEY,
  settings   JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT tenant_settings_tenant_id_required CHECK (btrim(tenant_id) <> ''),
  CONSTRAINT tenant_settings_settings_object CHECK (jsonb_typeof(settings) = 'object')
);

-- +goose Down
DROP TABLE IF EXISTS tenant_settings;
