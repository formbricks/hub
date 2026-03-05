-- +goose NO TRANSACTION
-- +goose Up
-- Indexes for optimal keyset (cursor) pagination on list endpoints.
-- CONCURRENTLY avoids blocking writes on large tables (requires NO TRANSACTION).
--
-- feedback_records: list/ListAfterCursor ORDER BY collected_at DESC, id ASC with tenant_id filter.
-- Replaces idx_feedback_records_tenant_collected_at with id for tie-break; drop the older one.
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_collected_at;
CREATE INDEX CONCURRENTLY idx_feedback_records_tenant_collected_at_id
  ON feedback_records (tenant_id, collected_at DESC, id);

-- webhooks: list/ListAfterCursor ORDER BY created_at DESC, id ASC with tenant_id filter.
CREATE INDEX CONCURRENTLY idx_webhooks_tenant_created_at_id
  ON webhooks (tenant_id, created_at DESC, id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_webhooks_tenant_created_at_id;

DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_collected_at_id;
CREATE INDEX CONCURRENTLY idx_feedback_records_tenant_collected_at
  ON feedback_records (tenant_id, collected_at);
