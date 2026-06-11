package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type TaxonomyRunStatus string

const (
	TaxonomyRunStatusPending   TaxonomyRunStatus = "pending"
	TaxonomyRunStatusRunning   TaxonomyRunStatus = "running"
	TaxonomyRunStatusSucceeded TaxonomyRunStatus = "succeeded"
	TaxonomyRunStatusFailed    TaxonomyRunStatus = "failed"
	TaxonomyRunStatusCanceled  TaxonomyRunStatus = "canceled"
)

type TaxonomyNodeType string

const (
	TaxonomyNodeTypeRoot   TaxonomyNodeType = "root"
	TaxonomyNodeTypeBranch TaxonomyNodeType = "branch"
	TaxonomyNodeTypeLeaf   TaxonomyNodeType = "leaf"
)

type TaxonomyScope struct {
	TenantID   string `json:"tenant_id"   validate:"required,no_null_bytes,min=1,max=255"`
	SourceType string `json:"source_type" validate:"required,no_null_bytes,min=1,max=255"`
	SourceID   string `json:"source_id"   validate:"required,no_null_bytes,min=1,max=255"`
	FieldID    string `json:"field_id"    validate:"required,no_null_bytes,min=1,max=255"`
}

type TaxonomyFieldOption struct {
	TenantID       string `json:"tenant_id"`
	SourceType     string `json:"source_type"`
	SourceID       string `json:"source_id"`
	SourceName     string `json:"source_name,omitempty"`
	FieldID        string `json:"field_id"`
	FieldLabel     string `json:"field_label,omitempty"`
	RecordCount    int    `json:"record_count"`
	EmbeddingCount int    `json:"embedding_count"`
}

type TaxonomyFieldsResponse struct {
	Data []TaxonomyFieldOption `json:"data"`
}

type TaxonomyRun struct {
	ID             uuid.UUID         `json:"id"`
	TenantID       string            `json:"tenant_id"`
	SourceType     string            `json:"source_type"`
	SourceID       string            `json:"source_id"`
	FieldID        string            `json:"field_id"`
	FieldLabel     *string           `json:"field_label,omitempty"`
	Status         TaxonomyRunStatus `json:"status"`
	Params         json.RawMessage   `json:"params,omitempty"`
	Metrics        json.RawMessage   `json:"metrics,omitempty"`
	RecordCount    int               `json:"record_count"`
	EmbeddingCount int               `json:"embedding_count"`
	ClusterCount   int               `json:"cluster_count"`
	NodeCount      int               `json:"node_count"`
	Error          *string           `json:"error,omitempty"`
	StartedAt      *time.Time        `json:"started_at,omitempty"`
	FinishedAt     *time.Time        `json:"finished_at,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type CreateTaxonomyRunRequest struct {
	TaxonomyScope
	FieldLabel *string `json:"field_label,omitempty" validate:"omitempty,no_null_bytes"`
	ActorID    *string `json:"actor_id,omitempty"    validate:"omitempty,no_null_bytes,min=1,max=255"`
}

type CreateTaxonomyRunResponse struct {
	Run        TaxonomyRun `json:"run"`
	InProgress bool        `json:"in_progress"`
}

type ListTaxonomyRunsFilters struct {
	TenantID   string `form:"tenant_id"   validate:"required,no_null_bytes,min=1,max=255"`
	SourceType string `form:"source_type" validate:"omitempty,no_null_bytes,min=1,max=255"`
	SourceID   string `form:"source_id"   validate:"omitempty,no_null_bytes,min=1,max=255"`
	FieldID    string `form:"field_id"    validate:"omitempty,no_null_bytes,min=1,max=255"`
	Limit      int    `form:"limit"       validate:"omitempty,min=1,max=100"`
}

type ListTaxonomyRunsResponse struct {
	Data []TaxonomyRun `json:"data"`
}

type TaxonomyCluster struct {
	ID         uuid.UUID       `json:"id"`
	RunID      uuid.UUID       `json:"run_id"`
	ClusterKey int             `json:"cluster_key"`
	Label      *string         `json:"label,omitempty"`
	LLMLabel   *string         `json:"llm_label,omitempty"`
	Keywords   json.RawMessage `json:"keywords,omitempty"`
	Size       int             `json:"size"`
	IsOutlier  bool            `json:"is_outlier"`
	Metrics    json.RawMessage `json:"metrics,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type TaxonomyNode struct {
	ID            uuid.UUID        `json:"id"`
	RunID         uuid.UUID        `json:"run_id"`
	ParentID      *uuid.UUID       `json:"parent_id,omitempty"`
	ClusterID     *uuid.UUID       `json:"cluster_id,omitempty"`
	NodeType      TaxonomyNodeType `json:"node_type"`
	Label         string           `json:"label"`
	OriginalLabel *string          `json:"original_label,omitempty"`
	Description   *string          `json:"description,omitempty"`
	Level         int              `json:"level"`
	SortOrder     int              `json:"sort_order"`
	Metadata      json.RawMessage  `json:"metadata,omitempty"`
	RemovedAt     *time.Time       `json:"removed_at,omitempty"`
	RemovedBy     *string          `json:"removed_by,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Children      []TaxonomyNode   `json:"children,omitempty"`
}

type TaxonomyTreeResponse struct {
	Run  TaxonomyRun   `json:"run"`
	Root *TaxonomyNode `json:"root"`
}

type TaxonomyRunInputRecord struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id"`
	FieldLabel       string    `json:"field_label,omitempty"`
	ValueText        string    `json:"value_text"`
	Embedding        []float32 `json:"embedding"`
}

type TaxonomyRunInputResponse struct {
	Run     TaxonomyRun              `json:"run"`
	Records []TaxonomyRunInputRecord `json:"records"`
}

type TaxonomyResultCluster struct {
	ClusterKey int             `json:"cluster_key"`
	Label      *string         `json:"label,omitempty"`
	LLMLabel   *string         `json:"llm_label,omitempty"`
	Keywords   json.RawMessage `json:"keywords,omitempty"`
	Size       int             `json:"size"`
	IsOutlier  bool            `json:"is_outlier"`
	Metrics    json.RawMessage `json:"metrics,omitempty"`
}

type TaxonomyResultMembership struct {
	ClusterKey       int             `json:"cluster_key"`
	FeedbackRecordID uuid.UUID       `json:"feedback_record_id"`
	Confidence       *float64        `json:"confidence,omitempty"`
	Distance         *float64        `json:"distance,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
}

type TaxonomyResultNode struct {
	NodeKey     string           `json:"node_key"    validate:"required,no_null_bytes,min=1,max=255"`
	ParentKey   *string          `json:"parent_key,omitempty" validate:"omitempty,no_null_bytes,min=1,max=255"`
	ClusterKey  *int             `json:"cluster_key,omitempty"`
	NodeType    TaxonomyNodeType `json:"node_type"   validate:"required,oneof=root branch leaf"`
	Label       string           `json:"label"       validate:"required,no_null_bytes,min=1"`
	Description *string          `json:"description,omitempty" validate:"omitempty,no_null_bytes"`
	Level       int              `json:"level"       validate:"min=0"`
	SortOrder   int              `json:"sort_order"`
	Metadata    json.RawMessage  `json:"metadata,omitempty"`
}

type TaxonomyRunResultRequest struct {
	Metrics     json.RawMessage            `json:"metrics,omitempty"`
	Clusters    []TaxonomyResultCluster    `json:"clusters"    validate:"required,dive"`
	Memberships []TaxonomyResultMembership `json:"memberships" validate:"required,dive"`
	Nodes       []TaxonomyResultNode       `json:"nodes"       validate:"required,dive"`
}

type TaxonomyRunFailedRequest struct {
	Error string `json:"error" validate:"required,no_null_bytes,min=1,max=2000"`
}

type RenameTaxonomyNodeRequest struct {
	TenantID string `json:"tenant_id" validate:"required,no_null_bytes,min=1,max=255"`
	ActorID  string `json:"actor_id"  validate:"required,no_null_bytes,min=1,max=255"`
	Label    string `json:"label"     validate:"required,no_null_bytes,min=1"`
}

type TaxonomyNodeRecordsFilters struct {
	TenantID string `form:"tenant_id" validate:"required,no_null_bytes,min=1,max=255"`
	Limit    int    `form:"limit"     validate:"omitempty,min=1,max=100"`
}

type RemoveTaxonomyNodeFilters struct {
	TenantID string `form:"tenant_id" validate:"required,no_null_bytes,min=1,max=255"`
	ActorID  string `form:"actor_id"  validate:"required,no_null_bytes,min=1,max=255"`
}

type TaxonomyNodeRecordsResponse struct {
	Data  []FeedbackRecord `json:"data"`
	Limit int              `json:"limit"`
}
