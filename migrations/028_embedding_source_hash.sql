-- +goose up
-- Stale-vector repair and raw-embedding failure state (ENG-2792).
--
-- embeddings.source_hash fingerprints the record content a vector was computed from: the columns
-- its input kind reads (field_label and value_text for the raw search embedding, plus
-- value_text_translated for the taxonomy embedding). Postgres computes it from the row the write
-- has just locked and checked, so it always describes exactly what was embedded, and the repair
-- sweep compares it with the record's current content to find vectors that describe text the record
-- no longer has. Content, not updated_at: feedback_records.updated_at moves on every write,
-- including enrichment writes and re-imports that change nothing, and keying on it would re-embed
-- whole sources for nothing.
--
-- Vectors written before this migration keep a NULL hash and are trusted as current: nothing here
-- can tell which of them are stale, and re-embedding every legacy vector on deploy would cost one
-- provider call each. They are re-verified on the record's next content change, or on demand per
-- tenant with backfill-embeddings.
--
-- Adding a nullable column without a default is a catalog-only change; no rewrite, no backfill.
ALTER TABLE embeddings ADD COLUMN IF NOT EXISTS source_hash BYTEA;

-- The raw search embedding ('embedding') now records durable failure state like the taxonomy one,
-- keyed the same way: context_key is the exact model and source_updated_at the record revision,
-- so a marker from an old model or revision cannot suppress new work. Without it the repair sweep
-- could not tell a transient failure (retry after the cooldown) from a terminal one (never retry
-- until the content changes), and would spend a provider call per terminal record on every sweep.
ALTER TABLE feedback_record_enrichment_failures
  DROP CONSTRAINT IF EXISTS feedback_record_enrichment_failures_enrichment_valid;

ALTER TABLE feedback_record_enrichment_failures
  ADD CONSTRAINT feedback_record_enrichment_failures_enrichment_valid CHECK (
    (enrichment IN ('translation', 'sentiment', 'emotions')
      AND context_key IS NULL AND source_updated_at IS NULL)
    OR
    (enrichment IN ('taxonomy_embedding', 'embedding')
      AND NULLIF(btrim(context_key), '') IS NOT NULL AND source_updated_at IS NOT NULL)
  );

-- +goose down
-- Raw-embedding markers are advisory and the old constraint cannot represent them; a future
-- attempt recreates whatever is still true.
DELETE FROM feedback_record_enrichment_failures
WHERE enrichment = 'embedding';

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

ALTER TABLE embeddings DROP COLUMN IF EXISTS source_hash;
