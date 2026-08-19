package models

// FeedbackRecordsPurgeCounts is the repository result for a tenant's feedback-records purge.
//
// Narrower than TenantDataDeleteCounts in exactly one respect: this purge removes the tenant's
// feedback records, everything derived from them, and the taxonomy built on them, but leaves the
// tenant's *configuration* — its webhooks and its settings — alone.
type FeedbackRecordsPurgeCounts struct {
	// The taxonomy goes with the records it describes. Keeping it would leave a tree whose nodes
	// all count zero while the run header still reported its original record_count, and which the
	// dashboard hides behind its minimum-records gate anyway — so it would be neither usable nor
	// visible, only misleading if the dataset later refilled.
	TenantTaxonomyDeleteCounts

	DeletedFeedbackRecords int64
	DeletedEmbeddings      int64
}

// FeedbackRecordsPurgeAcceptedResponse is the 202 body for
// DELETE /v1/tenants/{tenant_id}/feedback-records.
//
// The purge runs as a background job, so there is no deleted count to report: the work has been
// accepted, not done. Callers observe progress from the data instead, by polling
// GET /v1/feedback-records/count for the tenant until it reaches zero. Deliberately no count is
// echoed here — one taken at enqueue time is stale the moment ingestion continues, and would read
// as a promise about how much this purge will remove.
type FeedbackRecordsPurgeAcceptedResponse struct {
	TenantID string `json:"tenant_id"`
	Status   string `json:"status"`
	Message  string `json:"message"`
}

// FeedbackRecordsPurgeStatusAccepted is the only status the purge endpoint reports. The purge is
// fire-and-forget from the API's point of view; completion is observed from the data.
const FeedbackRecordsPurgeStatusAccepted = "accepted"
