-- +goose NO TRANSACTION
-- +goose up
CREATE UNIQUE INDEX CONCURRENTLY feedback_records_id_tenant_uidx
  ON feedback_records (id, tenant_id);

-- +goose down
DROP INDEX CONCURRENTLY IF EXISTS feedback_records_id_tenant_uidx;
