-- +goose NO TRANSACTION
-- +goose up
-- Index support for filtering feedback records on embedded data (ENG-6925).
--
-- Formbricks dashboards filter charts on the embedded-data keys a survey collected
-- (metadata.embedded_data.<key>). Cube turns each filter into a containment predicate on the
-- embedded_data object, and a dashboard with two filters ANDs two of them:
--
--   metadata->'embedded_data' @> '{"plan":"pro"}' AND metadata->'embedded_data' @> '{"region":"eu"}'
--
-- No existing index can answer that, so every chart on a filtered dashboard scanned all of the
-- tenant's rows and read each one's metadata. Measured on 600k rows with two embedded filters:
-- 1.5-2.2 s without this index, 53-180 ms with it.
--
-- jsonb_path_ops rather than the default jsonb_ops: it supports only @> (and the jsonpath
-- operators), which is the one operator the filters use, and in exchange stores one hash per
-- key/value path instead of an entry per key and per value, so it is smaller and more selective
-- for containment. A key-existence query (`?`) cannot use it; nothing issues one.
--
-- The expression is metadata->'embedded_data', not metadata, so the index carries only embedded
-- data and not the rest of the metadata object, and a query must use exactly this expression to
-- match it.
--
-- Case-sensitive by construction: @> compares JSON values exactly, so the filters match values
-- as collected (no LOWER/TRIM normalization, unlike the static string fields).
--
-- Write cost: one more index on the write path. Since 027 indexed updated_at no update to this
-- table is HOT, so every insert and every update also inserts here, including updates that leave
-- metadata unchanged. GIN inserts go through the pending list (fastupdate) and are flushed in
-- batches, so the per-row cost stays small; records without embedded data index as a single NULL
-- entry.
--
-- Runs without a transaction because of CONCURRENTLY, so writes continue during the build, and is
-- re-runnable: DROP-then-CREATE replaces the INVALID index an interrupted CREATE INDEX CONCURRENTLY
-- leaves behind (see 021). Measured build: about 4 s and 26 MB on 1.7M rows of production-shaped
-- data, 3 s and 22 MB on 1.5M synthetic rows (70% carrying seven embedded keys).
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_embedded_data;
CREATE INDEX CONCURRENTLY idx_feedback_records_embedded_data
  ON feedback_records USING gin ((metadata->'embedded_data') jsonb_path_ops);

-- +goose down
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_embedded_data;
