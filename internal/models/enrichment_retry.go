package models

// RetryOutcome says what happened to one enrichment in a retry request.
type RetryOutcome string

const (
	// RetryOutcomeCleared means terminal markers were removed and the records will be picked up by
	// the next reconcile sweep.
	RetryOutcomeCleared RetryOutcome = "cleared"
	// RetryOutcomeCoolingDown means the tenant cleared this enrichment too recently. RetryAfter
	// carries the wait.
	RetryOutcomeCoolingDown RetryOutcome = "cooling_down"
	// RetryOutcomeDisabled means the enrichment is not running for this tenant, so clearing its
	// markers would queue work the worker's gate would skip. DisabledReason says which gate.
	RetryOutcomeDisabled RetryOutcome = "disabled"
)

// EnrichmentRetryResult is one enrichment's outcome.
//
// Per-enrichment rather than one status for the whole request, because the answers genuinely
// differ: a tenant can have sentiment cleared, emotions cooling down and translation switched off
// in the same call, and collapsing that into a single code would lose the two the caller can act on.
type EnrichmentRetryResult struct {
	Enrichment string       `json:"enrichment"`
	Outcome    RetryOutcome `json:"outcome"`
	// Cleared is how many records had their terminal marker removed. Zero with outcome "cleared"
	// is a real answer: nothing was permanently failed.
	Cleared int64 `json:"cleared"`
	// RetryAfterSeconds is set only for "cooling_down". The endpoint returns the remaining wait
	// rather than silently doing nothing, because a caller that cannot tell "cleared nothing" from
	// "refused" will just call again.
	RetryAfterSeconds int64 `json:"retry_after_seconds,omitempty"`
	// DisabledReason is set only for "disabled", and carries the same values the status endpoint
	// uses so a consumer needs one vocabulary rather than two.
	DisabledReason DisabledReason `json:"disabled_reason,omitempty"`
}

// EnrichmentRetryResponse is the body of an accepted retry request.
//
// 202 rather than 200: clearing the markers is synchronous, but the retrying is not — the records
// are picked up by the next reconcile sweep, so the work this request causes has not happened when
// it returns.
type EnrichmentRetryResponse struct {
	TenantID string                  `json:"tenant_id"`
	Results  []EnrichmentRetryResult `json:"results"`
}
