-- +goose Up
-- Webhooks are tenant-owned dispatch configuration. Disable legacy global rows and
-- prevent new NULL or empty tenant IDs without blocking this migration on old data.
UPDATE webhooks
SET
  enabled = false,
  disabled_reason = COALESCE(disabled_reason, 'Disabled by migration: tenant_id is required for webhook isolation'),
  disabled_at = COALESCE(disabled_at, NOW()),
  updated_at = NOW()
WHERE tenant_id IS NULL OR btrim(tenant_id) = '';

ALTER TABLE webhooks
  ADD CONSTRAINT webhooks_tenant_id_required
  CHECK (tenant_id IS NOT NULL AND btrim(tenant_id) <> '') NOT VALID;

-- +goose Down
ALTER TABLE webhooks
  DROP CONSTRAINT IF EXISTS webhooks_tenant_id_required;
