package models

import (
	"time"

	"github.com/google/uuid"
)

// The enrichment names, declared once rather than spelled out at each use. They are shared by the
// marker table's `enrichment` column (migration 022's CHECK, widened by 024), the `enrichment`
// metric label, and the sweep's per-enrichment specs — but not every name is all three: a name is a
// marker value only for a pipeline that can write one, and a metric label for every pipeline that
// can be trimmed or can fail. Do not write a marker under a name the CHECK does not admit; the
// insert fails on the failure path, which is the worst place to find out.
const (
	EnrichmentNameTranslation = "translation"
	EnrichmentNameSentiment   = "sentiment"
	EnrichmentNameEmotions    = "emotions"
	// EnrichmentNameEmbedding is the raw search embedding — the vector behind semantic search and
	// similar-feedback. Its failure markers (migration 028) are bound to a model and a revision, like
	// the taxonomy embedding's, so the repair sweep can tell transient failures from terminal ones.
	EnrichmentNameEmbedding = "embedding"
	// EnrichmentNameTaxonomyEmbedding is the translated-text embedding consumed by taxonomy. Its
	// markers (migration 024) are bound to a model and a revision.
	EnrichmentNameTaxonomyEmbedding = "taxonomy_embedding"
)

// The two non-terminal reasons. Both mean "did not succeed this time"; they differ in which half
// of the job failed, which is the difference between an incident with the provider and an incident
// with our own database. The CHECK in migration 022 restricts non-terminal rows to these.
const (
	// EnrichmentFailureReasonProviderError is recorded when an enrichment exhausted its retries
	// against a provider failure that was NOT determined by the record's content — a 5xx, a
	// timeout, a response that could not be parsed.
	EnrichmentFailureReasonProviderError = "provider_error"
	// EnrichmentFailureReasonWriteFailed is recorded when the provider answered but the result
	// could not be persisted before the attempts ran out. The record is exactly as un-enriched as
	// a provider failure leaves it, and telling the two apart is what stops an operator hunting a
	// provider outage that never happened.
	EnrichmentFailureReasonWriteFailed = "write_failed"
)

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
	// ContextKey and SourceUpdatedAt bind a taxonomy-embedding marker to the exact model and
	// record revision that failed. They are empty for the other enrichment types. Without both,
	// a terminal failure from an old model or content revision could suppress repair forever.
	ContextKey      string
	SourceUpdatedAt *time.Time
}
