-- +goose NO TRANSACTION
-- +goose up
-- Index support for the new query filters and sort control on GET /v1/feedback-records and
-- .../count (ENG-2059). The endpoints gain repeatable identity filters, created_at / value_date /
-- value_number / sentiment_score ranges, source_name and language equality, the sentiment /
-- emotions / translation presence filters, and sort=collected_at|created_at.
--
-- Most of those need nothing new. The eight identity filters keep the indexes their single-value
-- form used: `col = ANY($1)` is a ScalarArrayOpExpr, which the planner turns into an index scan
-- just as it does `col = $1`. Sentiment is served by the partial (tenant_id, sentiment) index from
-- 014 and emotions by the partial GIN from 015 — array overlap `&&` is a GIN array_ops operator
-- and is strict, so the planner can prove `emotions IS NOT NULL` and still match that index's
-- predicate.
--
-- Three filters had no usable index, and all three degrade the same way: the planner can only
-- satisfy them by walking idx_feedback_records_tenant_collected_at_id and discarding rows, which
-- gets worse exactly as a tenant grows large enough to care.
--
-- Runs without a transaction (like the other index migrations) so it never holds a long lock on
-- feedback_records, the primary high-write table: every index is built CONCURRENTLY. Every
-- statement is RE-RUNNABLE — under NO TRANSACTION each statement auto-commits while goose records
-- the version only at the end, so an interrupted deploy (a pod eviction mid CREATE INDEX
-- CONCURRENTLY additionally leaves an INVALID index) re-runs the whole file cleanly.
-- DROP-then-CREATE (not IF NOT EXISTS) so a re-run replaces the INVALID leftover instead of
-- keeping it.

-- created_at is the only range filter on a NOT NULL column with zero coverage, and it is also the
-- reason sort control exists: filtering created_at while ordering by collected_at cannot use
-- idx_feedback_records_tenant_collected_at_id for the ordering, so a narrow created_at window over
-- a wide collected_at range degrades to a residual scan. Sorting by the filtered column is the
-- fix, and the (DESC, id) shape mirrors 006 so this one index serves both the filter and
-- `ORDER BY created_at DESC, id ASC` without a sort step.
--
-- `ORDER BY created_at ASC, id ASC` is served by reading this index backward for the created_at
-- prefix plus an incremental sort on id. created_at is a server NOW() per single-row transaction
-- at microsecond resolution, so those tie groups are effectively size 1 and the residual sort is
-- free — which is why the mirrored ASC index is deliberately NOT created.
--
-- NOTE: this is not the index 006 dropped. That one was (tenant_id, collected_at) without id.
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_created_at_id;
CREATE INDEX CONCURRENTLY idx_feedback_records_tenant_created_at_id
  ON feedback_records (tenant_id, created_at DESC, id);

-- value_date is a range filter on a column only `date` fields populate. PARTIAL keeps it off the
-- ingestion write path for the overwhelmingly common text/rating record, matching the sparse-column
-- shape used by 014, 015 and 018.
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_value_date;
CREATE INDEX CONCURRENTLY idx_feedback_records_tenant_value_date
  ON feedback_records (tenant_id, value_date) WHERE value_date IS NOT NULL;

-- source_name is the only new string-equality filter with no index of any kind: source_id has had
-- one since 001, source_name never did. PARTIAL for the same sparsity reason — it is optional on
-- ingestion.
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_source_name;
CREATE INDEX CONCURRENTLY idx_feedback_records_tenant_source_name
  ON feedback_records (tenant_id, source_name) WHERE source_name IS NOT NULL;

-- Deliberately NOT indexed here — feedback_records already carries ~22 indexes and every addition
-- is permanent write amplification on the ingestion path:
--   * language — VARCHAR(10) with a handful of distinct values per tenant. An index whose most
--     common value covers a large fraction of the table will not be chosen, and the tenant
--     predicate already does the narrowing.
--   * value_number — idx_feedback_records_value_number (001) exists. It is not tenant-prefixed, so
--     it can only be bitmap-ANDed with idx_feedback_records_tenant_id, but that is an acceptable
--     plan and a tenant-prefixed replacement needs an EXPLAIN, not a guess.
--   * has_sentiment / has_emotions / has_translation in their FALSE form — they select the NULL
--     rows that the partial indexes from 013/014/015 deliberately exclude. Their TRUE form is
--     already served by those indexes.

-- +goose down
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_source_name;
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_value_date;
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_tenant_created_at_id;
