package models

import (
	"time"

	"github.com/google/uuid"
)

// KnowledgeRecord represents a single knowledge record
type KnowledgeRecord struct {
	ID        uuid.UUID `json:"id"`
	Content   string    `json:"content"`
	TenantID  *string   `json:"tenant_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateKnowledgeRecordRequest represents the request to create a knowledge record
type CreateKnowledgeRecordRequest struct {
	Content  string  `json:"content" validate:"required,no_null_bytes,min=1,max=10000"`
	TenantID *string `json:"tenant_id,omitempty" validate:"omitempty,no_null_bytes,max=255"`
}

// UpdateKnowledgeRecordRequest represents the request to update a knowledge record
// Only content can be updated
type UpdateKnowledgeRecordRequest struct {
	Content *string `json:"content,omitempty" validate:"omitempty,no_null_bytes,min=1,max=10000"`
}

// ListKnowledgeRecordsFilters represents filters for listing knowledge records
type ListKnowledgeRecordsFilters struct {
	TenantID *string `form:"tenant_id" validate:"omitempty,no_null_bytes"`
	Limit    int     `form:"limit" validate:"omitempty,min=1,max=1000"`
	Offset   int     `form:"offset" validate:"omitempty,min=0"`
}

// ListKnowledgeRecordsResponse represents the response for listing knowledge records
type ListKnowledgeRecordsResponse struct {
	Data   []KnowledgeRecord `json:"data"`
	Total  int64             `json:"total"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
}

// BulkDeleteKnowledgeRecordsFilters represents query parameters for bulk delete operation
type BulkDeleteKnowledgeRecordsFilters struct {
	TenantID string `form:"tenant_id" validate:"required,no_null_bytes,min=1"`
}

// BulkDeleteKnowledgeRecordsResponse represents the response for bulk delete operation
type BulkDeleteKnowledgeRecordsResponse struct {
	DeletedCount int64  `json:"deleted_count"`
	Message      string `json:"message"`
}
