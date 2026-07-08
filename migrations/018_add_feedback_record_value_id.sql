-- +goose NO TRANSACTION
-- +goose up
-- value_id is the stable id of the selected option in the source system (a Formbricks survey choice
-- id, a matrix column id, etc.), stored alongside the human-readable value_text so selected-choice
-- answers keep a durable identity across label edits and response-language differences (ENG-1671).
-- Hub treats it as an opaque string (no validation against any source registry); it is NULL for
-- non-choice fields, free-text/"other" answers, and non-Formbricks sources (CSV, integrations),
-- where value_text remains the fallback identity.
--
-- Runs without a transaction (like the other index migrations) so it never holds a long lock on
-- feedback_records (the primary, high-write table):
--   * ADD COLUMN of a nullable column with no default is metadata-only (instant).
--   * the index is built CONCURRENTLY (no ACCESS EXCLUSIVE / write block).
-- Every statement is also RE-RUNNABLE: under NO TRANSACTION each statement auto-commits while goose
-- records the version only at the end, so an interrupted deploy (e.g. pod eviction mid CREATE INDEX
-- CONCURRENTLY, which additionally leaves an INVALID index) re-runs the whole file cleanly.
ALTER TABLE feedback_records ADD COLUMN IF NOT EXISTS value_id VARCHAR(255);

-- Targeted lookups are "all records for option X of field F in tenant T" (the label-edit update
-- flow) and the value_id list filter, i.e. equality on (tenant_id, field_id, value_id). value_id is
-- sparse (NULL for text/CSV/non-choice rows), so a PARTIAL index over the non-NULL rows keeps it
-- small and off the ingestion write path for the common text-only record — mirroring the table's
-- other sparse-column partial indexes (sentiment, emotions). The composite is left-prefix usable
-- for (tenant_id, field_id) too. DROP-then-CREATE (not IF NOT EXISTS) so a re-run after an
-- interrupted build replaces the INVALID leftover instead of keeping it.
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_field_value_id;
CREATE INDEX CONCURRENTLY idx_feedback_records_tenant_field_value_id
  ON feedback_records (tenant_id, field_id, value_id) WHERE value_id IS NOT NULL;

-- +goose down
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_field_value_id;
ALTER TABLE feedback_records DROP COLUMN IF EXISTS value_id;
