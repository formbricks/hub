package models

// TenantDataDeleteCounts is the repository result for a tenant data purge.
type TenantDataDeleteCounts struct {
	DeletedFeedbackRecords int64
	DeletedEmbeddings      int64
	DeletedWebhooks        int64
}

// TenantDataDeleteResult is the service result for a tenant data purge.
type TenantDataDeleteResult struct {
	TenantDataDeleteCounts

	TenantID string
}

// TenantDataDeleteResponse represents the response for deleting all Hub-owned tenant data.
type TenantDataDeleteResponse struct {
	TenantID               string `json:"tenant_id"`
	DeletedFeedbackRecords int64  `json:"deleted_feedback_records"`
	DeletedEmbeddings      int64  `json:"deleted_embeddings"`
	DeletedWebhooks        int64  `json:"deleted_webhooks"`
	Message                string `json:"message"`
}
