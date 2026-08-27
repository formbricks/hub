package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/formbricks/hub/internal/huberrors"
)

// EnrichmentRetryRepository clears terminal failure markers and holds the cooldown that bounds how
// often that may happen.
type EnrichmentRetryRepository struct {
	db *pgxpool.Pool
}

// NewEnrichmentRetryRepository creates an enrichment retry repository.
func NewEnrichmentRetryRepository(db *pgxpool.Pool) *EnrichmentRetryRepository {
	return &EnrichmentRetryRepository{db: db}
}

// clearTerminalMarkersSQL claims the cooldown window and, only if the claim succeeded, deletes the
// tenant's terminal markers — one statement, three properties baked in:
//
//   - THE TENANT WRITE LOCK GATES EVERYTHING (AGENTS.md: every tenant-owned mutation). The gate CTE
//     try-acquires the shared advisory lock exactly as recordEnrichmentFailureSQL does; refused
//     means a purge holds the tenant, and nothing below runs. Skipping it would reopen the
//     purge/write race for this table, which the OTHER repository writing it carefully closes.
//
//   - THE CLAIM IS THE COOLDOWN, ATOMICALLY. The upsert's WHERE takes the window decision inside
//     the row lock: two concurrent requests serialize on the cooldown row, the second sees the
//     first's fresh stamp and claims nothing. A separate read-then-clear was tried first and is a
//     textbook TOCTOU — both callers observe "expired" and both clear, which quietly loosens the
//     one bound this table exists to enforce.
//
//   - THE DELETE RUNS ONLY IF THE CLAIM DID. Deleting is gated on the claim CTE, so a refused
//     caller cannot clear anything, and a successful caller cannot clear without spending their
//     window. Clearing an empty set still spends it — otherwise "clear, find nothing, clear again"
//     is an unbounded loop with an extra step.
//
// The victims join keeps fr.tenant_id as the authoritative boundary and adds f.tenant_id as an
// ADDITIONAL narrowing predicate so idx_enrichment_failures_tenant_enrichment applies — the same
// additional-not-instead-of rule migration 022 documents for the counting queries.
const clearTerminalMarkersSQL = `
	WITH gate AS (
		SELECT pg_try_advisory_xact_lock_shared(hashtextextended($4, 0)) AS locked
	),
	claim AS (
		INSERT INTO enrichment_retry_cooldowns (tenant_id, enrichment, cleared_at)
		SELECT $1, $2, NOW() FROM gate WHERE gate.locked
		ON CONFLICT (tenant_id, enrichment) DO UPDATE SET cleared_at = NOW()
			WHERE enrichment_retry_cooldowns.cleared_at <= NOW() - $3::interval
		RETURNING 1
	),
	victims AS (
		SELECT f.feedback_record_id
		FROM feedback_record_enrichment_failures f
		JOIN feedback_records fr ON fr.id = f.feedback_record_id
		WHERE fr.tenant_id = $1 AND f.tenant_id = $1 AND f.enrichment = $2 AND f.terminal
			AND EXISTS (SELECT 1 FROM claim)
	),
	deleted AS (
		DELETE FROM feedback_record_enrichment_failures
		WHERE enrichment = $2 AND feedback_record_id IN (SELECT feedback_record_id FROM victims)
		RETURNING 1
	)
	SELECT (SELECT gate.locked FROM gate),
		EXISTS (SELECT 1 FROM claim),
		(SELECT count(*) FROM deleted)`

// ClearTerminalMarkers atomically claims the cooldown window and removes the tenant's terminal
// markers for one enrichment. claimed=false with a nil error means the window has not expired —
// the caller reads the remaining wait separately for its response.
func (r *EnrichmentRetryRepository) ClearTerminalMarkers(
	ctx context.Context, tenantID, enrichment string, window time.Duration,
) (claimed bool, cleared int64, err error) {
	var locked bool

	err = r.db.QueryRow(ctx, clearTerminalMarkersSQL,
		tenantID, enrichment, window, TenantWriteLockKey(tenantID),
	).Scan(&locked, &claimed, &cleared)
	if err != nil {
		return false, 0, fmt.Errorf("clear terminal enrichment failures: %w", err)
	}

	if !locked {
		return false, 0, huberrors.NewTenantWriteConflictError(
			"a purge holds this tenant's write lock; retry later")
	}

	return claimed, cleared, nil
}

// cooldownRemainingSQL reports how much of the cooldown window is left, or no row when the tenant
// has never cleared this enrichment.
//
// Computed in SQL against NOW() rather than compared in Go against a fetched timestamp: the
// database's clock is the one that wrote cleared_at, and on a multi-replica deployment the API
// servers' clocks are not guaranteed to agree with it or with each other. A few seconds of skew
// either way would let a caller through early or hold them late.
const cooldownRemainingSQL = `
	SELECT GREATEST($3::interval - (NOW() - cleared_at), INTERVAL '0')
	FROM enrichment_retry_cooldowns
	WHERE tenant_id = $1 AND enrichment = $2`

// CooldownRemaining returns how long until this tenant may clear this enrichment again. Zero means
// now. Informational only — the authoritative window decision is ClearTerminalMarkers' claim.
func (r *EnrichmentRetryRepository) CooldownRemaining(
	ctx context.Context, tenantID, enrichment string, window time.Duration,
) (time.Duration, error) {
	var remaining time.Duration

	err := r.db.QueryRow(ctx, cooldownRemainingSQL, tenantID, enrichment, window).Scan(&remaining)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Never cleared, so nothing to wait for.
			return 0, nil
		}

		return 0, fmt.Errorf("read enrichment retry cooldown: %w", err)
	}

	return remaining, nil
}
