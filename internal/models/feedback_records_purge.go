package models

// FeedbackRecordsPurgeCounts is the repository result for a tenant's feedback-records purge.
//
// Deliberately narrower than TenantDataDeleteCounts: this purge removes the tenant's feedback
// records and the data derived from them, and leaves the tenant's taxonomy structure, webhooks and
// settings in place. Cluster memberships are counted because they are derived per record (a
// membership is "this record belongs to that cluster"); the clusters, nodes and runs themselves
// survive.
type FeedbackRecordsPurgeCounts struct {
	DeletedFeedbackRecords int64
	DeletedEmbeddings      int64

	// DeletedTaxonomyClusterMemberships counts the record→cluster links dropped with the records.
	// The clusters keep existing; they simply end up with no members.
	DeletedTaxonomyClusterMemberships int64
}

// FeedbackRecordsPurgeRequest is the body for POST /v1/feedback-records/purge.
//
// tenant_id is required and lives in the body rather than being an optional filter on the
// collection delete: an omitted or empty value must never widen a narrower deletion into a
// tenant-wide one.
type FeedbackRecordsPurgeRequest struct {
	TenantID string `json:"tenant_id" validate:"required,no_null_bytes,min=1,max=255"`
}

// FeedbackRecordsPurgeAcceptedResponse is the 202 body for POST /v1/feedback-records/purge.
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

// FeedbackRecordsPurgeStatusAccepted is the only status POST /v1/feedback-records/purge reports.
// The purge is fire-and-forget from the API's point of view; completion is observed from the data.
const FeedbackRecordsPurgeStatusAccepted = "accepted"
