-- +goose up
-- Add submission_id to feedback_records for grouping records belonging to one logical submission.
-- Enables idempotent multi-field ingestion and simpler grouped reads (e.g. GET ?submission_id=...).
-- Mandatory (NOT NULL): every record must belong to a submission; use e.g. field_id if single-field.

ALTER TABLE feedback_records
  ADD COLUMN submission_id VARCHAR(255) NOT NULL;

-- Index for filtering by submission_id (and tenant-scoped list by submission)
CREATE INDEX idx_feedback_records_submission_id ON feedback_records(submission_id);
CREATE INDEX idx_feedback_records_tenant_submission_id ON feedback_records(tenant_id, submission_id);

-- One value per field_id per submission per tenant (supports idempotent webhook retries).
-- NULLS NOT DISTINCT (PG 15+): treat NULL tenant_id as equal so duplicate (NULL, submission_id, field_id) conflicts.
CREATE UNIQUE INDEX idx_feedback_records_tenant_submission_field
  ON feedback_records(tenant_id, submission_id, field_id) NULLS NOT DISTINCT;

-- +goose down
DROP INDEX IF EXISTS idx_feedback_records_tenant_submission_field;
DROP INDEX IF EXISTS idx_feedback_records_tenant_submission_id;
DROP INDEX IF EXISTS idx_feedback_records_submission_id;
ALTER TABLE feedback_records DROP COLUMN IF EXISTS submission_id;
