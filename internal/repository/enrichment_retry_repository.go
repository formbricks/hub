package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

// clearTerminalMarkersSQL deletes one tenant's terminal markers for one enrichment and records the
// clear, in a single statement so the cooldown cannot be missed if the caller dies between the two.
//
// The cooldown row is written even when no marker was deleted. A caller who clears an empty
// terminal set has still spent their turn — otherwise "clear, find nothing, clear again" is an
// unbounded loop that the cooldown was added to prevent, just with an extra step.
//
// The tenant boundary is feedback_records.tenant_id, joined through, not the markers' own
// denormalized column — the same rule the counting queries follow (see migration 022).
const clearTerminalMarkersSQL = `
	WITH victims AS (
		SELECT f.feedback_record_id
		FROM feedback_record_enrichment_failures f
		JOIN feedback_records fr ON fr.id = f.feedback_record_id
		WHERE fr.tenant_id = $1 AND f.enrichment = $2 AND f.terminal
	),
	deleted AS (
		DELETE FROM feedback_record_enrichment_failures
		WHERE enrichment = $2 AND feedback_record_id IN (SELECT feedback_record_id FROM victims)
		RETURNING 1
	),
	cooled AS (
		INSERT INTO enrichment_retry_cooldowns (tenant_id, enrichment, cleared_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (tenant_id, enrichment) DO UPDATE SET cleared_at = NOW()
		RETURNING 1
	)
	SELECT (SELECT count(*) FROM deleted)`

// ClearTerminalMarkers removes a tenant's terminal markers for one enrichment and stamps the
// cooldown. Returns how many markers were cleared.
func (r *EnrichmentRetryRepository) ClearTerminalMarkers(
	ctx context.Context, tenantID, enrichment string,
) (int64, error) {
	var cleared int64

	if err := r.db.QueryRow(ctx, clearTerminalMarkersSQL, tenantID, enrichment).Scan(&cleared); err != nil {
		return 0, fmt.Errorf("clear terminal enrichment failures: %w", err)
	}

	return cleared, nil
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
// now.
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
