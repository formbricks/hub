-- +goose NO TRANSACTION
-- +goose up
-- Durable, cause-classified record of enrichment failures (ENG-2375).
--
-- Failure state has until now lived only in River: a job that exhausts its attempts is discarded,
-- and River reaps discarded rows after its retention window (7 days by default; the Hub overrides
-- only CompletedJobRetentionPeriod). So "how many records failed" was a number that silently reset
-- to zero, and "which of them can never succeed" could not be asked at all -- a content-filter
-- block and a 503 were indistinguishable by the time anything read them.
--
-- Two things depend on being able to tell those apart. The API reports them separately, so the UI
-- can say "3 failed -- retry" and "1 can't be processed" rather than showing a count that drains
-- on its own. And the reconciler, which re-enqueues everything eligible-but-not-done, must exclude
-- the permanent ones or it re-runs them on every tick, forever, at a provider call each time.
--
-- ROWS ARE ADVISORY, NOT AUTHORITATIVE. `failed` is reported as "a row exists AND the record is
-- not done", never from this table alone. That is deliberate: a later success needs no cleanup
-- write, so the enrichment hot path stays free of bookkeeping, and a stale row can never inflate
-- the count. The sweeper garbage-collects dead rows lazily.
--
-- THE TENANT BOUNDARY IS feedback_records.tenant_id, NOT THE COLUMN HERE. tenant_id is
-- denormalized onto this table solely so the per-tenant index below is possible. Counting queries
-- join on feedback_record_id and keep `fr.tenant_id = $n` as their only tenant predicate. Do not
-- "simplify" a query to filter on this column instead: that would give the tenant boundary two
-- sources of truth, and the guard would only ever be as good as the write path that populated it.
--
-- ON DELETE CASCADE rather than an explicit delete in the tenant purge, which diverges from the
-- convention in tenant_data_repository.go ("the purge never relies on cascades"). That rule exists
-- for the taxonomy tables, which would be genuinely ORPHANED by a feedback_records delete because
-- they do not cascade, and whose per-table counts are reported in the purge response. Neither
-- applies here: this table cascades correctly, and a count of removed failure markers is internal
-- derived state that no caller of the purge has a use for. Rows carry tenant_id, though, so a
-- broken cascade would leave tenant-scoped data behind -- covered by an integration test asserting
-- none survive a purge.
--
-- Runs without a transaction because of the CONCURRENTLY index build below.
CREATE TABLE IF NOT EXISTS feedback_record_enrichment_failures (
  feedback_record_id UUID        NOT NULL REFERENCES feedback_records(id) ON DELETE CASCADE,
  -- Which enrichment failed. Matches the names used by the status endpoint and the metric label.
  enrichment         VARCHAR(32) NOT NULL,
  -- Denormalized for the index below only -- see the tenant-boundary note above.
  tenant_id          VARCHAR(255) NOT NULL,
  failed_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  -- How many attempts were spent before giving up. Diagnostic only.
  attempts           INTEGER     NOT NULL DEFAULT 0,
  -- terminal = retrying cannot help, because the outcome is a property of the record's own text.
  terminal           BOOLEAN     NOT NULL,
  -- Bounded set, never the provider's error string. Refusal text is model-generated but routinely
  -- paraphrases the input, so storing it would put customer feedback at rest here and from here
  -- into the API. Raw provider text stays in logs, already truncated at the client.
  reason             VARCHAR(32) NOT NULL,
  PRIMARY KEY (feedback_record_id, enrichment)
);

ALTER TABLE feedback_record_enrichment_failures
  DROP CONSTRAINT IF EXISTS feedback_record_enrichment_failures_enrichment_valid;
-- Scoped to the three record-level enrichments the status endpoint reports. Embeddings is the
-- fourth pipeline and shares these failure modes, so recording its failures here later is a
-- plausible extension -- and would need a migration to widen this list, not just worker code.
ALTER TABLE feedback_record_enrichment_failures
  ADD CONSTRAINT feedback_record_enrichment_failures_enrichment_valid CHECK (
    enrichment IN ('translation', 'sentiment', 'emotions')
  );

-- The reason set and the terminal flag are one decision, so the database enforces that they agree.
-- Without this a bug could write terminal = true with reason 'provider_error', and the reconciler
-- would then permanently skip a record whose only problem was a transient 503.
--
-- The non-terminal arm carries two values because a record can be left un-enriched by either half
-- of the job: the provider never answered, or it answered and the result could not be written.
-- Both are retryable and the counts treat them identically; they are distinguished because an
-- operator seeing a spike of one should not go looking at the other.
ALTER TABLE feedback_record_enrichment_failures
  DROP CONSTRAINT IF EXISTS feedback_record_enrichment_failures_reason_valid;
ALTER TABLE feedback_record_enrichment_failures
  ADD CONSTRAINT feedback_record_enrichment_failures_reason_valid CHECK (
    (terminal AND reason IN ('content_filter', 'refusal', 'length', 'recitation'))
    OR
    (NOT terminal AND reason IN ('provider_error', 'write_failed'))
  );

-- Serves the per-tenant `failed` / `failed_terminal` counts. terminal is in the key rather than a
-- predicate because both halves are queried; the table only ever holds failures, so there is no
-- large uninteresting majority to exclude with a partial index.
DROP INDEX CONCURRENTLY IF EXISTS idx_enrichment_failures_tenant_enrichment;
CREATE INDEX CONCURRENTLY idx_enrichment_failures_tenant_enrichment
  ON feedback_record_enrichment_failures (tenant_id, enrichment, terminal);

-- +goose down
DROP INDEX CONCURRENTLY IF EXISTS idx_enrichment_failures_tenant_enrichment;
DROP TABLE IF EXISTS feedback_record_enrichment_failures;
