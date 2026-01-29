// Package jobs provides River job workers for async processing tasks.
package jobs

import "github.com/google/uuid"

// EmbeddingJobArgs contains the arguments for an embedding generation job.
type EmbeddingJobArgs struct {
	// RecordID is the UUID of the record to generate embeddings for
	RecordID uuid.UUID `json:"record_id"`

	// RecordType identifies which table the record belongs to
	// Valid values: "feedback_record", "topic", "knowledge_record"
	RecordType string `json:"record_type"`

	// Text is the content to generate embeddings for
	// For topics, this is the hierarchical path (e.g., "Performance > API")
	Text string `json:"text"`

	// TenantID is used for tenant-isolated topic assignment after embedding generation
	// Only used for feedback_record type
	TenantID *string `json:"tenant_id,omitempty"`
}

// Kind returns the job type identifier for River
func (EmbeddingJobArgs) Kind() string { return "embedding" }

// Record type constants
const (
	RecordTypeFeedback  = "feedback_record"
	RecordTypeTopic     = "topic"
	RecordTypeKnowledge = "knowledge_record"
)
