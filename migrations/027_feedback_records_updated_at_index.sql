-- +goose NO TRANSACTION
-- +goose up
-- Index support for the updated_since / updated_until filters on GET /v1/feedback-records and
-- .../count (ENG-3420).
--
-- updated_since exists for incremental extraction: "every record created or changed since my last
-- run". That query is narrow by design — a sync that runs hourly matches a sliver of the tenant —
-- and without an index the planner can only satisfy it by walking
-- idx_feedback_records_tenant_collected_at_id and discarding rows, so every page of every sync
-- reads the whole tenant. That is the degradation migration 021 indexed created_at against, and it
-- gets worse exactly as a tenant grows large enough to need incremental extraction at all.
--
-- (tenant_id, updated_at), with no id and no DESC: unlike created_at, updated_at is never a sort
-- key (it is mutable, see models.SortField), so this index only has to find the matching rows. The
-- listing still orders by collected_at or created_at; a narrow match set is fetched here and
-- sorted, and a wide one falls back to walking the ordering index, which the planner picks by cost.
--
-- Write cost, accepted deliberately: updated_at changes on every PATCH and every enrichment write,
-- so each of those updates now maintains this index too. Sentiment, emotions and PATCH writes
-- already touch indexed columns and were not HOT updates anyway; a translation write touched none
-- and could be HOT until now. One extra index entry per enrichment write is the price of an
-- extraction query that does not scan the tenant.
--
-- Runs without a transaction because of CONCURRENTLY, and is re-runnable: DROP-then-CREATE replaces
-- the INVALID index an interrupted CREATE INDEX CONCURRENTLY leaves behind (see 021).
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_updated_at;
CREATE INDEX CONCURRENTLY idx_feedback_records_tenant_updated_at
  ON feedback_records (tenant_id, updated_at);

-- +goose down
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_updated_at;
