package models

import "time"

// DisabledReason names the gate that switched an enrichment off for a tenant. It exists because
// `enabled: false` alone is unactionable: an operator who has not configured a provider, a tenant
// that turned the enrichment off, and a tenant with no resolvable translation target are three
// different problems with three different fixes, and the UI cannot tell them apart from a bool.
//
// The value set is closed and small so it is safe as a metric label and as a UI translation key.
type DisabledReason string

const (
	// DisabledReasonNotConfigured means the deployment has no provider+model for this enrichment,
	// so it is off for every tenant. Fixed by the operator, not by the tenant.
	DisabledReasonNotConfigured DisabledReason = "not_configured"
	// DisabledReasonSwitchedOff means the deployment is configured but this tenant has explicitly
	// turned the enrichment off (the tri-state per-directory switch). Sentiment and emotions only.
	DisabledReasonSwitchedOff DisabledReason = "switched_off"
	// DisabledReasonNoTargetLanguage means translation is configured but neither the tenant's own
	// target_language nor the deployment default resolves, so there is nothing to translate into.
	// Translation only — it has no on/off switch, an absent target IS the off state.
	DisabledReasonNoTargetLanguage DisabledReason = "no_target_language"
)

// EnrichmentTypeStatus is one tenant's progress for a single record-level enrichment
// (translation, sentiment, or emotions). Enabled reports whether the enrichment is both
// deployment-configured and switched on for the tenant (for translation: has a resolvable
// target language). Eligible is the number of feedback records that qualify for the
// enrichment; Done is how many have been enriched. The UI derives "in progress" as
// Eligible - Done. When Enabled is false, Eligible and Done are zero (no work will run).
//
// DisabledReason is present exactly when Enabled is false, and absent otherwise — the two are
// derived from one decision (see enrichmentTypeStatus), so they cannot contradict each other.
type EnrichmentTypeStatus struct {
	Enabled  bool  `json:"enabled"`
	Eligible int64 `json:"eligible"`
	Done     int64 `json:"done"`
	// Failed is eligible records whose last attempt gave up but which a retry could still rescue —
	// a provider outage, a timeout. FailedTerminal is those the provider will never accept,
	// because the outcome is a property of the record's own text: a content-policy block, a
	// refusal, an input past the model's limit.
	//
	// The split is the difference between "3 failed — retry" and "1 can't be processed", and it is
	// also what stops anything re-enqueueing unfinished work from looping on the second group
	// forever. Both count only while the record is still un-enriched, so a later success removes it
	// from either without a cleanup write.
	Failed         int64          `json:"failed"`
	FailedTerminal int64          `json:"failed_terminal"`
	DisabledReason DisabledReason `json:"disabled_reason,omitempty"`
}

// EnrichmentStatusResponse reports a tenant's enrichment progress across the record-level
// enrichments. Counts are directory-level totals; the response never includes record
// identifiers or feedback content.
type EnrichmentStatusResponse struct {
	TenantID string `json:"tenant_id"`
	// AsOf is when the counts were computed, not when the response was serialized. The endpoint is
	// polled, so a client holding two responses can derive throughput and an ETA from the change in
	// Done over the change in AsOf without the Hub computing either.
	AsOf        time.Time            `json:"as_of"`
	Translation EnrichmentTypeStatus `json:"translation"`
	Sentiment   EnrichmentTypeStatus `json:"sentiment"`
	Emotions    EnrichmentTypeStatus `json:"emotions"`
}
