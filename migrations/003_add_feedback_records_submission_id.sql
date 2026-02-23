-- +goose up
-- Add submission_id to feedback_records for grouping records belonging to one logical submission.
-- Enables idempotent multi-field ingestion and simpler grouped reads (e.g. GET ?submission_id=...).

ALTER TABLE feedback_records
  ADD COLUMN submission_id VARCHAR(255);

-- Index for filtering by submission_id (and tenant-scoped list by submission)
CREATE INDEX idx_feedback_records_submission_id ON feedback_records(submission_id);
CREATE INDEX idx_feedback_records_tenant_submission_id ON feedback_records(tenant_id, submission_id);

-- One value per field_id per submission per tenant (supports idempotent webhook retries).
-- Partial index: only enforce uniqueness when submission_id is set (backward compatible for NULL).
CREATE UNIQUE INDEX idx_feedback_records_tenant_submission_field
  ON feedback_records(tenant_id, submission_id, field_id)
  WHERE submission_id IS NOT NULL;

-- +goose down
DROP INDEX IF EXISTS idx_feedback_records_tenant_submission_field;
DROP INDEX IF EXISTS idx_feedback_records_tenant_submission_id;
DROP INDEX IF EXISTS idx_feedback_records_submission_id;
ALTER TABLE feedback_records DROP COLUMN IF EXISTS submission_id;
