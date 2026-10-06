-- +goose NO TRANSACTION
-- +goose up
-- Index support for the updated_since / updated_until filters on GET /v1/feedback-records and
-- .../count (ENG-3420).
--
-- updated_since exists for incremental extraction: "every record created or changed since my last
-- run". That query is narrow by design — a sync that runs hourly matches a sliver of the tenant —
-- and without an index the planner can only satisfy it by walking
-- idx_feedback_records_tenant_collected_at_id and discarding rows, or by a sequential scan of the
-- whole table (what it chose on a local 2M-row table), so every page of every sync reads at least
-- the whole tenant. That is the degradation migration 021 indexed created_at against, and it
-- gets worse exactly as a tenant grows large enough to need incremental extraction at all.
--
-- (tenant_id, updated_at), with no id and no DESC: unlike created_at, updated_at is never a sort
-- key (it is mutable, see models.SortField), so this index only has to find the matching rows. The
-- listing still orders by collected_at or created_at; a narrow match set is fetched here and
-- sorted, and a wide one falls back to walking the ordering index, which the planner picks by cost.
--
-- Write cost, accepted deliberately, and larger than "one more index": every writer sets updated_at,
-- so once it is indexed NO update to this table can be a HOT update, and a non-HOT update inserts
-- into every index on the table (29 with this one, counting the primary key), except the partial
-- indexes whose predicate the new row does not match. Sentiment and emotions writes, and PATCHes
-- touching indexed columns, were already non-HOT. Translation writes and metadata-only PATCHes were
-- not: each went from about zero index inserts to twenty-odd. Measured on a local 2M-row table,
-- translation-shaped single-row updates went from ~5.7-7.1k TPS (83-91% HOT) to ~1.1-1.2k TPS
-- (0% HOT). Accepted because enrichment is bound by LLM latency at tens of writes per second, far
-- below that ceiling, whereas without the index an updated_since query for a large tenant is a
-- sequential scan of the whole table, all tenants included (57-105 ms at 2M rows and growing with
-- total data, against ~0.1 ms with it). Expect more WAL and index bloat on translation-heavy deployments.
--
-- Runs without a transaction because of CONCURRENTLY, so writes continue during the build, and is
-- re-runnable: DROP-then-CREATE replaces the INVALID index an interrupted CREATE INDEX CONCURRENTLY
-- leaves behind (see 021). The build itself is a burst of WAL; locally it took ~10 s on 2M rows and
-- caused a checkpoint stall, so a large deployment should expect a short write-latency spike.
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_updated_at;
CREATE INDEX CONCURRENTLY idx_feedback_records_tenant_updated_at
  ON feedback_records (tenant_id, updated_at);

-- +goose down
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_updated_at;
