package models

// EnrichmentTypeStatus is one tenant's progress for a single record-level enrichment
// (translation, sentiment, or emotions). Enabled reports whether the enrichment is both
// deployment-configured and switched on for the tenant (for translation: has a resolvable
// target language). Eligible is the number of feedback records that qualify for the
// enrichment; Done is how many have been enriched. The UI derives "in progress" as
// Eligible - Done. When Enabled is false, Eligible and Done are zero (no work will run).
type EnrichmentTypeStatus struct {
	Enabled  bool  `json:"enabled"`
	Eligible int64 `json:"eligible"`
	Done     int64 `json:"done"`
}

// EnrichmentStatusResponse reports a tenant's enrichment progress across the record-level
// enrichments. Counts are directory-level totals; the response never includes record
// identifiers or feedback content.
type EnrichmentStatusResponse struct {
	TenantID    string               `json:"tenant_id"`
	Translation EnrichmentTypeStatus `json:"translation"`
	Sentiment   EnrichmentTypeStatus `json:"sentiment"`
	Emotions    EnrichmentTypeStatus `json:"emotions"`
}
