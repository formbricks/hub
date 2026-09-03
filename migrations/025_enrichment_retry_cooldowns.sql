-- +goose up
-- Cooldown state for the manual enrichment retry endpoint (ENG-2376).
--
-- The retry endpoint clears terminal failure markers so the reconciler will pick those records up
-- again. Terminal records are, by definition, the ones the provider has already refused -- so a
-- clear is a request to spend a provider call on every one of them, and most of those calls will
-- fail exactly as before.
--
-- Without a bound that is a cost-amplification primitive: call -> clear -> the sweep re-runs the
-- whole terminal set -> all fail terminally again -> call again, at (size of terminal set) provider
-- invocations per cycle, unbounded, from one authenticated caller. The Hub has no rate limiting
-- anywhere (internal/api/middleware is auth, cors, logging, problem, request_id), so nothing else
-- would stop it. The reconcile job's own uniqueness coalesces concurrent sweeps but does nothing
-- about sequential cycles.
--
-- One row per (tenant, enrichment) recording when the flags were last cleared bounds the
-- amplification to (terminal set / cooldown). The endpoint refuses inside the window and returns
-- the remaining wait rather than silently no-opping, because a caller that cannot tell "cleared
-- nothing" from "refused" will simply retry.
--
-- Deliberately its own table rather than a column on tenant_settings: this is operational state
-- about a request, not tenant configuration, and tenant_settings is what the enrichment gates read
-- on every status call.
CREATE TABLE IF NOT EXISTS enrichment_retry_cooldowns (
  tenant_id  VARCHAR(255) NOT NULL,
  -- Matches the enrichment names in migration 022's CHECK and the status endpoint.
  enrichment VARCHAR(32)  NOT NULL,
  cleared_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  PRIMARY KEY (tenant_id, enrichment)
);

ALTER TABLE enrichment_retry_cooldowns
  DROP CONSTRAINT IF EXISTS enrichment_retry_cooldowns_enrichment_valid;
ALTER TABLE enrichment_retry_cooldowns
  ADD CONSTRAINT enrichment_retry_cooldowns_enrichment_valid CHECK (
    enrichment IN ('translation', 'sentiment', 'emotions')
  );

-- No index beyond the primary key: every read and write is by the full (tenant_id, enrichment) key.
--
-- No foreign key to a tenants table either, because there isn't one -- a tenant is an id carried on
-- feedback records, so there is nothing to reference. That means these rows outlive a tenant data
-- purge, which is intentional: a cooldown is a few bytes, and dropping it on purge would hand back
-- a fresh budget to whoever just purged.

-- +goose down
DROP TABLE IF EXISTS enrichment_retry_cooldowns;
