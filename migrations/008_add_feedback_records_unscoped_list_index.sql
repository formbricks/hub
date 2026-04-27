-- +goose NO TRANSACTION
-- +goose Up
-- Supports unscoped keyset pagination for GET /v1/feedback-records when tenant_id is omitted.
CREATE INDEX CONCURRENTLY idx_feedback_records_collected_at_id
  ON feedback_records (collected_at DESC, id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_collected_at_id;
