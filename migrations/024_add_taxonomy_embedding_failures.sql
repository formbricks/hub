-- +goose Up
-- Taxonomy-only embeddings use the same durable failure state as the other enrichments. The
-- existing primary key, tenant index, reason set, retention, and purge cascade all apply
-- unchanged. The two nullable context columns prevent a marker from an old model or record
-- revision suppressing a new attempt.
ALTER TABLE feedback_record_enrichment_failures
  ADD COLUMN context_key TEXT,
  ADD COLUMN source_updated_at TIMESTAMPTZ;

ALTER TABLE feedback_record_enrichment_failures
  DROP CONSTRAINT IF EXISTS feedback_record_enrichment_failures_enrichment_valid;

ALTER TABLE feedback_record_enrichment_failures
  ADD CONSTRAINT feedback_record_enrichment_failures_enrichment_valid CHECK (
    (enrichment IN ('translation', 'sentiment', 'emotions')
      AND context_key IS NULL AND source_updated_at IS NULL)
    OR
    (enrichment = 'taxonomy_embedding'
      AND NULLIF(btrim(context_key), '') IS NOT NULL AND source_updated_at IS NOT NULL)
  );

-- +goose Down
-- Remove rows the old constraint cannot represent before restoring it. Failure markers are
-- advisory and can be reconstructed by a future attempt, so rollback remains safe.
DELETE FROM feedback_record_enrichment_failures
WHERE enrichment = 'taxonomy_embedding';

ALTER TABLE feedback_record_enrichment_failures
  DROP CONSTRAINT IF EXISTS feedback_record_enrichment_failures_enrichment_valid;

ALTER TABLE feedback_record_enrichment_failures
  ADD CONSTRAINT feedback_record_enrichment_failures_enrichment_valid CHECK (
    enrichment IN ('translation', 'sentiment', 'emotions')
  );

ALTER TABLE feedback_record_enrichment_failures
  DROP COLUMN IF EXISTS source_updated_at,
  DROP COLUMN IF EXISTS context_key;
