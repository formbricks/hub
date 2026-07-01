package models

// TenantDataDeleteCounts is the repository result for a tenant data purge.
type TenantDataDeleteCounts struct {
	DeletedFeedbackRecords int64
	DeletedEmbeddings      int64
	DeletedWebhooks        int64

	// Taxonomy artifacts are run-scoped Hub data removed explicitly during the
	// purge (not via feedback-record cascades). Each field is the exact row
	// count deleted from its taxonomy table for the tenant.
	DeletedTaxonomyRuns               int64
	DeletedTaxonomyClusters           int64
	DeletedTaxonomyClusterMemberships int64
	DeletedTaxonomyNodes              int64
	DeletedTaxonomyActiveRuns         int64
	DeletedTaxonomyNodeEvents         int64
}

// TenantDataDeleteResult is the service result for a tenant data purge.
type TenantDataDeleteResult struct {
	TenantDataDeleteCounts

	TenantID string
}

// TenantDataDeleteResponse represents the response for deleting all Hub-owned tenant data.
type TenantDataDeleteResponse struct {
	TenantID                          string `json:"tenant_id"`
	DeletedFeedbackRecords            int64  `json:"deleted_feedback_records"`
	DeletedEmbeddings                 int64  `json:"deleted_embeddings"`
	DeletedWebhooks                   int64  `json:"deleted_webhooks"`
	DeletedTaxonomyRuns               int64  `json:"deleted_taxonomy_runs"`
	DeletedTaxonomyClusters           int64  `json:"deleted_taxonomy_clusters"`
	DeletedTaxonomyClusterMemberships int64  `json:"deleted_taxonomy_cluster_memberships"`
	DeletedTaxonomyNodes              int64  `json:"deleted_taxonomy_nodes"`
	DeletedTaxonomyActiveRuns         int64  `json:"deleted_taxonomy_active_runs"`
	DeletedTaxonomyNodeEvents         int64  `json:"deleted_taxonomy_node_events"`
	Message                           string `json:"message"`
}
