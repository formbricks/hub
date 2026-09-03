-- +goose NO TRANSACTION
-- +goose up
-- Index support for the reconciler's pending-set queries (ENG-2376).
--
-- The sweep asks, every tick: the newest N eligible records still missing this enrichment. Without
-- these, that is a scan and top-N sort of the ENTIRE pending set per tick — enable sentiment on a
-- 50M-record deployment and every 5 minutes re-sorts tens of millions of rows to emit 1,000, for
-- the whole months-long drain. With them the planner walks the index in ORDER BY order and stops
-- at the LIMIT.
--
-- Partial on the pending predicate, so each index holds only un-enriched rows and SHRINKS as the
-- backlog drains — the same shape migration 016 chose for the backfill lookups, with the ordering
-- key the sweep needs. The residual conditions the queries add (the btrim non-blank check, tenant
-- gates, the terminal-marker and in-flight anti-joins) are filters on top; what matters is that
-- the query's WHERE implies the index predicate, which these do.
--
-- Translation deliberately gets NO index here. Its not-done predicate is
-- `translation_lang_key IS DISTINCT FROM <effective target>`, which depends on a per-tenant value
-- joined at run time — Postgres can only use a partial index when the query provably implies its
-- predicate, and IS DISTINCT FROM a parameter cannot imply IS NULL. Narrowing the sweep to
-- IS NULL would make it indexable but would break the load-bearing parity with the status
-- endpoint's pending definition (a record translated to a since-changed target would be reported
-- pending yet never swept). Accepted cost, revisit if translation backlogs dominate.
--
-- Runs without a transaction because of CONCURRENTLY.
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_sentiment_pending;
CREATE INDEX CONCURRENTLY idx_feedback_records_sentiment_pending
  ON feedback_records (collected_at DESC, id DESC)
  WHERE field_type = 'text' AND value_text IS NOT NULL AND sentiment IS NULL;

DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_emotions_pending;
CREATE INDEX CONCURRENTLY idx_feedback_records_emotions_pending
  ON feedback_records (collected_at DESC, id DESC)
  WHERE field_type = 'text' AND value_text IS NOT NULL
    AND emotions_classified_at IS NULL AND emotions IS NULL;

-- +goose down
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_sentiment_pending;
DROP INDEX CONCURRENTLY IF EXISTS idx_feedback_records_emotions_pending;
