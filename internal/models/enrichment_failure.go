package models

import "github.com/google/uuid"

// EnrichmentFailureReasonProviderError is the reason recorded when an enrichment exhausted its
// retries against a failure that was NOT determined by the record's content — a 5xx, a timeout, a
// response that could not be parsed. It is the only reason a non-terminal row may carry, enforced
// by the CHECK in migration 022.
const EnrichmentFailureReasonProviderError = "provider_error"

// EnrichmentFailure is one record's failed enrichment, as written by the worker after the last
// attempt is spent.
//
// Terminal separates "will never succeed" from "did not succeed this time". Only the first is a
// property of the record's own text, and only the first may be skipped by anything that
// re-enqueues unfinished work — a transient failure left permanently skipped is a record silently
// abandoned.
//
// The row is advisory: readers report a failure only when the record is also still un-enriched,
// so a later success needs no cleanup write and cannot be contradicted by a stale row.
type EnrichmentFailure struct {
	FeedbackRecordID uuid.UUID
	// TenantID is the record's own tenant, read from the record rather than from anything the
	// caller supplied. It exists on this table to support the per-tenant index and must never be
	// used as the tenant boundary in a query — see migration 022.
	TenantID   string
	Enrichment string
	// Terminal and Reason are one decision. The database enforces that they agree, so callers must
	// not set Terminal true with EnrichmentFailureReasonProviderError or the write is rejected.
	Terminal bool
	Reason   string
	// Attempts spent before giving up. Diagnostic only.
	Attempts int
}
