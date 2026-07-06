-- +goose NO TRANSACTION
-- +goose up
-- Classify-backfill support (ENG-1613). The sentiment/emotions backfills page eligible records
-- whose classification is still absent:
--
--   SELECT id FROM feedback_records
--   WHERE field_type = 'text' AND value_text IS NOT NULL AND btrim(value_text) <> ''
--     AND {sentiment|emotions} IS NULL AND id > $1 ORDER BY id LIMIT $2
--
-- The 014/015 partial indexes cover the IS NOT NULL complement (enriched-row lookups), which the
-- planner cannot use for an IS NULL predicate — so each keyset page scanned feedback_records (the
-- high-write primary table, already sizable since 0.7.0) filtering for the unenriched rows.
--
-- These partial indexes on (id) over exactly the eligible-and-unenriched set turn each keyset page
-- into an ordered index range scan. They stay small in steady state (a row leaves the index the
-- moment it is enriched — sentiment/emotions set non-NULL) and shrink to near-empty once a backfill
-- drains; only text records are indexed, so non-text answers (nps/rating/csat/...) that are
-- permanently NULL never bloat them. `id` is the leading (and only) column so `id > $1 ORDER BY id`
-- is served directly.
--
-- Runs without a transaction (like the other index migrations) so CREATE INDEX CONCURRENTLY never
-- holds a long lock on feedback_records. DROP-then-CREATE (not IF NOT EXISTS) makes each statement
-- re-runnable: an interrupted build leaves an INVALID index that the re-run replaces. goose records
-- the version only at file end, so an interrupted deploy re-runs the whole file.
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_sentiment_backfill;
CREATE INDEX CONCURRENTLY idx_feedback_records_sentiment_backfill
  ON feedback_records (id)
  WHERE field_type = 'text' AND value_text IS NOT NULL AND sentiment IS NULL;

DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_emotions_backfill;
CREATE INDEX CONCURRENTLY idx_feedback_records_emotions_backfill
  ON feedback_records (id)
  WHERE field_type = 'text' AND value_text IS NOT NULL AND emotions IS NULL;

-- +goose down
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_sentiment_backfill;
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_emotions_backfill;
