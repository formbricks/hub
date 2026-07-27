-- +goose NO TRANSACTION
-- +goose up
-- Emotion completion marker (ENG-1670). `emotions` cannot express "classified, found nothing":
-- the 015 CHECK rejects the empty array, so a successful classification that detects no emotion is
-- persisted as NULL — indistinguishable from "not classified yet". Anything deriving progress from
-- the data (the enrichment-status endpoint, the backlog gauge) therefore counted those records as
-- permanently pending, and the classify backfill re-sent them to the LLM on every run.
--
-- emotions_classified_at records WHEN the classifier last produced a result for the record,
-- independently of whether that result had any labels. It is processing state, deliberately kept
-- out of the API surface (not in feedbackRecordColumns), so no response shape or SDK type changes.
-- The eager-clear on a value_text edit nulls it alongside `emotions`, so an edited record correctly
-- returns to "not classified".
--
-- Deliberately NOT backfilled: a bulk UPDATE would rewrite every row of the primary high-write
-- table (WAL + bloat) for no benefit. Readers instead treat a non-NULL `emotions` as proof of
-- completion for pre-existing rows (see the enrichment-status query), which leaves only historical
-- classified-empty rows unresolved; the classify backfill re-processes those once and stamps them.
--
-- Runs without a transaction like the sibling enrichment migrations. ADD COLUMN of a nullable
-- column with no default is metadata-only (instant, no table rewrite, no long lock), and
-- IF NOT EXISTS keeps the statement re-runnable after an interrupted deploy.
ALTER TABLE feedback_records
  ADD COLUMN IF NOT EXISTS emotions_classified_at TIMESTAMPTZ;

-- +goose down
ALTER TABLE feedback_records
  DROP COLUMN IF EXISTS emotions_classified_at;
