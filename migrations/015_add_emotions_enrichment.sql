-- +goose NO TRANSACTION
-- +goose up
-- Emotion enrichment (ENG-1573): multi-label emotion classification of open-text feedback.
-- emotions is a server-generated array set by the async emotion worker; it is NULL until a record
-- is enriched (or emotions is disabled / the record is ineligible / no emotion was detected) and
-- is never an empty array (NULL is the single absence sentinel). It complements sentiment -- an
-- additional field, not a replacement. Keep the label pool in sync with models.EmotionValue (Go)
-- and the OpenAPI enum.
--
-- Runs without a transaction (like the other index migrations) so it never holds a long
-- ACCESS EXCLUSIVE lock on feedback_records (the primary, high-write table):
--   * ADD COLUMN of a nullable column with no default is metadata-only (instant).
--   * the CHECKs are added NOT VALID (instant, no scan; immediately enforced for new/updated
--     rows) and VALIDATEd as a separate, auto-committed step that takes only SHARE UPDATE
--     EXCLUSIVE, so reads and writes proceed during the verification scan.
--   * the index is built CONCURRENTLY.
ALTER TABLE feedback_records
  ADD COLUMN emotions TEXT[];

-- Every element must be one of the fixed Ekman-6 pool (subset containment). NULL is allowed (not
-- yet enriched); the empty array is rejected by the guard below, so "no emotion detected" is
-- stored as NULL rather than {}.
ALTER TABLE feedback_records
  ADD CONSTRAINT feedback_records_emotions_valid CHECK (
    emotions IS NULL OR emotions <@ ARRAY['joy', 'anger', 'sadness', 'fear', 'surprise', 'disgust']::text[]
  ) NOT VALID;

-- Reject the empty array so absence has a single representation (NULL). cardinality() (not
-- array_length) is deliberate: array_length('{}', 1) is NULL, and a CHECK treats NULL as
-- satisfied, so an empty array would slip past array_length > 0; cardinality('{}') is 0, which
-- fails the check as intended.
ALTER TABLE feedback_records
  ADD CONSTRAINT feedback_records_emotions_non_empty CHECK (
    emotions IS NULL OR cardinality(emotions) > 0
  ) NOT VALID;

ALTER TABLE feedback_records VALIDATE CONSTRAINT feedback_records_emotions_valid;
ALTER TABLE feedback_records VALIDATE CONSTRAINT feedback_records_emotions_non_empty;

-- Emotions is sparse (NULL until enriched) and queried by containment (records tagged with a
-- given emotion, e.g. emotions @> ARRAY['anger']), so a partial GIN index serves those lookups
-- while staying small and keeping the ingestion-path write overhead minimal (NULL rows are not
-- indexed). Reads are tenant-scoped, so the planner bitmap-ANDs this with the tenant filter.
CREATE INDEX CONCURRENTLY idx_feedback_records_emotions
  ON feedback_records USING GIN (emotions) WHERE emotions IS NOT NULL;

-- +goose down
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_emotions;
ALTER TABLE feedback_records
  DROP CONSTRAINT IF EXISTS feedback_records_emotions_non_empty,
  DROP CONSTRAINT IF EXISTS feedback_records_emotions_valid,
  DROP COLUMN IF EXISTS emotions;
