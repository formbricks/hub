package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// TaxonomyRunStatus is the persisted lifecycle state for a taxonomy generation run.
//
// Lifecycle contract:
//   - pending: Hub accepted the manual request and persisted a run.
//   - running: Hub handed the run to the taxonomy service.
//   - succeeded: the taxonomy service persisted artifacts and Hub activated the run.
//   - failed: the run ended with a sanitized error and optional failure code.
//   - canceled: reserved for future user/operator cancellation.
//
// Allowed transitions are pending -> running|failed|canceled and
// running -> succeeded|failed|canceled. Terminal states must not be overwritten.
// Insufficient input and service availability are represented as failure/error
// codes, not as additional persisted statuses.
type TaxonomyRunStatus string

// Taxonomy run statuses.
const (
	TaxonomyRunStatusPending   TaxonomyRunStatus = "pending"
	TaxonomyRunStatusRunning   TaxonomyRunStatus = "running"
	TaxonomyRunStatusSucceeded TaxonomyRunStatus = "succeeded"
	TaxonomyRunStatusFailed    TaxonomyRunStatus = "failed"
	TaxonomyRunStatusCanceled  TaxonomyRunStatus = "canceled"
)

// TaxonomyRunFailureCode is a machine-readable reason for a failed taxonomy run or prerequisite error.
type TaxonomyRunFailureCode string

// Taxonomy run failure codes.
const (
	TaxonomyRunFailureCodeInsufficientData   TaxonomyRunFailureCode = "insufficient_data"
	TaxonomyRunFailureCodeServiceUnavailable TaxonomyRunFailureCode = "service_unavailable"
	TaxonomyRunFailureCodeGenerationFailed   TaxonomyRunFailureCode = "generation_failed"
	TaxonomyRunFailureCodeInvalidOutput      TaxonomyRunFailureCode = "invalid_output"
	TaxonomyRunFailureCodeInternalError      TaxonomyRunFailureCode = "internal_error"
)

// TaxonomyNodeType describes a taxonomy node's position in the tree.
type TaxonomyNodeType string

// Taxonomy node types.
const (
	TaxonomyNodeTypeRoot   TaxonomyNodeType = "root"
	TaxonomyNodeTypeBranch TaxonomyNodeType = "branch"
	TaxonomyNodeTypeLeaf   TaxonomyNodeType = "leaf"
)

// TaxonomyScopeType identifies how broadly a taxonomy run reads feedback input.
type TaxonomyScopeType string

// Taxonomy scope types.
const (
	TaxonomyScopeTypeField     TaxonomyScopeType = "field"
	TaxonomyScopeTypeDirectory TaxonomyScopeType = "directory"
)

// TaxonomyScope identifies the feedback input that a taxonomy run covers.
//
// SourceID is optional: feedback_records.source_id is nullable, so feedback may have no
// attributed source. An empty SourceID is the canonical "no source" bucket and matches
// feedback records whose source_id is NULL or blank.
type TaxonomyScope struct {
	ScopeType  TaxonomyScopeType `json:"scope_type,omitempty" validate:"omitempty,oneof=field directory"`
	TenantID   string            `json:"tenant_id"            validate:"required,no_null_bytes,min=1,max=255"`
	SourceType string            `json:"source_type"          validate:"omitempty,no_null_bytes,min=1,max=255"`
	SourceID   string            `json:"source_id"            validate:"omitempty,no_null_bytes,max=255"`
	FieldID    string            `json:"field_id"             validate:"omitempty,no_null_bytes,min=1,max=255"`
}

// TaxonomyFieldOption describes a feedback field that can be used for taxonomy generation.
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

// TaxonomyFieldsResponse contains taxonomy-capable field options.
type TaxonomyFieldsResponse struct {
	Data []TaxonomyFieldOption `json:"data"`
}

// TaxonomyRun is a persisted taxonomy generation run.
type TaxonomyRun struct {
	ID             uuid.UUID               `json:"id"`
	ScopeType      TaxonomyScopeType       `json:"scope_type"`
	TenantID       string                  `json:"tenant_id"`
	SourceType     string                  `json:"source_type"`
	SourceID       string                  `json:"source_id"`
	FieldID        string                  `json:"field_id"`
	FieldLabel     *string                 `json:"field_label,omitempty"`
	Status         TaxonomyRunStatus       `json:"status"`
	Params         json.RawMessage         `json:"params,omitempty"`
	Metrics        json.RawMessage         `json:"metrics,omitempty"`
	RecordCount    int                     `json:"record_count"`
	EmbeddingCount int                     `json:"embedding_count"`
	ClusterCount   int                     `json:"cluster_count"`
	NodeCount      int                     `json:"node_count"`
	Error          *string                 `json:"error,omitempty"`
	ErrorCode      *TaxonomyRunFailureCode `json:"error_code,omitempty"`
	StartedAt      *time.Time              `json:"started_at,omitempty"`
	FinishedAt     *time.Time              `json:"finished_at,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
}

// CreateTaxonomyRunRequest starts a manual taxonomy generation run.
type CreateTaxonomyRunRequest struct {
	TaxonomyScope

	FieldLabel *string `json:"field_label,omitempty" validate:"omitempty,no_null_bytes"`
	ActorID    *string `json:"actor_id,omitempty"    validate:"omitempty,no_null_bytes,min=1,max=255"`
}

// CreateTaxonomyRunResponse returns the created or already-running taxonomy run.
type CreateTaxonomyRunResponse struct {
	Run        TaxonomyRun `json:"run"`
	InProgress bool        `json:"in_progress"`
}

// ListTaxonomyRunsFilters scopes taxonomy run history queries.
//
// SourceID is a tri-state filter: nil means "no source_id filter", while a non-nil
// pointer (including a pointer to "") filters by that exact value. An empty string
// scopes history to the canonical "no source" bucket — runs whose source_id is "" —
// which a plain string filter cannot express (it cannot tell "unset" from "empty").
type ListTaxonomyRunsFilters struct {
	ScopeType  TaxonomyScopeType `form:"scope_type"  validate:"omitempty,oneof=field directory"`
	TenantID   string            `form:"tenant_id"   validate:"required,no_null_bytes,min=1,max=255"`
	SourceType string            `form:"source_type" validate:"omitempty,no_null_bytes,min=1,max=255"`
	SourceID   *string           `form:"source_id"   validate:"omitempty,no_null_bytes,max=255"`
	FieldID    string            `form:"field_id"    validate:"omitempty,no_null_bytes,min=1,max=255"`
	Limit      int               `form:"limit"       validate:"omitempty,min=1,max=100"`
}

// ListTaxonomyRunsResponse contains taxonomy run history.
type ListTaxonomyRunsResponse struct {
	Data []TaxonomyRun `json:"data"`
}

// TaxonomyCluster is a generated feedback cluster for a taxonomy run.
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

// TaxonomyNode is a generated or edited node in a taxonomy tree.
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

// TaxonomyTreeResponse returns a run and its taxonomy tree.
type TaxonomyTreeResponse struct {
	Run  TaxonomyRun   `json:"run"`
	Root *TaxonomyNode `json:"root"`
}

// TaxonomyRunInputRecord is a feedback record and embedding used by the taxonomy service.
type TaxonomyRunInputRecord struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id"`
	SourceType       string    `json:"source_type,omitempty"`
	SourceID         string    `json:"source_id,omitempty"`
	FieldID          string    `json:"field_id,omitempty"`
	FieldLabel       string    `json:"field_label,omitempty"`
	ValueText        string    `json:"value_text"`
	Embedding        []float32 `json:"embedding"`
}

// TaxonomyRunInputResponse contains run-scoped input for the taxonomy service.
type TaxonomyRunInputResponse struct {
	Run     TaxonomyRun              `json:"run"`
	Records []TaxonomyRunInputRecord `json:"records"`
}

// TaxonomyResultCluster is a generated cluster returned by the taxonomy service.
type TaxonomyResultCluster struct {
	ClusterKey int             `json:"cluster_key"`
	Label      *string         `json:"label,omitempty"`
	LLMLabel   *string         `json:"llm_label,omitempty"`
	Keywords   json.RawMessage `json:"keywords,omitempty"`
	Size       int             `json:"size"`
	IsOutlier  bool            `json:"is_outlier"`
	Metrics    json.RawMessage `json:"metrics,omitempty"`
}

// TaxonomyResultMembership maps a feedback record to a generated cluster.
type TaxonomyResultMembership struct {
	ClusterKey       int             `json:"cluster_key"`
	FeedbackRecordID uuid.UUID       `json:"feedback_record_id"`
	Confidence       *float64        `json:"confidence,omitempty"`
	Distance         *float64        `json:"distance,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
}

// TaxonomyResultNode is a generated taxonomy tree node returned by the taxonomy service.
type TaxonomyResultNode struct {
	NodeKey     string           `json:"node_key"              validate:"required,no_null_bytes,min=1,max=255"`
	ParentKey   *string          `json:"parent_key,omitempty"  validate:"omitempty,no_null_bytes,min=1,max=255"`
	ClusterKey  *int             `json:"cluster_key,omitempty"`
	NodeType    TaxonomyNodeType `json:"node_type"             validate:"required,oneof=root branch leaf"`
	Label       string           `json:"label"                 validate:"required,no_null_bytes,min=1"`
	Description *string          `json:"description,omitempty" validate:"omitempty,no_null_bytes"`
	Level       int              `json:"level"                 validate:"min=0"`
	SortOrder   int              `json:"sort_order"`
	Metadata    json.RawMessage  `json:"metadata,omitempty"`
}

// TaxonomyRunResultRequest persists generated taxonomy artifacts.
type TaxonomyRunResultRequest struct {
	Metrics     json.RawMessage            `json:"metrics,omitempty"`
	Clusters    []TaxonomyResultCluster    `json:"clusters"          validate:"required,dive"`
	Memberships []TaxonomyResultMembership `json:"memberships"       validate:"required,dive"`
	Nodes       []TaxonomyResultNode       `json:"nodes"             validate:"required,dive"`
}

// TaxonomyRunFailedRequest records a taxonomy run failure.
type TaxonomyRunFailedRequest struct {
	Error     string                 `json:"error"                validate:"required,no_null_bytes,min=1,max=2000"`
	ErrorCode TaxonomyRunFailureCode `json:"error_code,omitempty" validate:"omitempty,oneof=insufficient_data service_unavailable generation_failed invalid_output internal_error"` //nolint:lll // Validator oneof values are space-delimited.
}

// RenameTaxonomyNodeRequest renames a generated taxonomy node.
type RenameTaxonomyNodeRequest struct {
	TenantID string `json:"tenant_id" validate:"required,no_null_bytes,min=1,max=255"`
	ActorID  string `json:"actor_id"  validate:"required,no_null_bytes,min=1,max=255"`
	Label    string `json:"label"     validate:"required,no_null_bytes,min=1"`
}

// TaxonomyNodeRecordsFilters scopes taxonomy node feedback record drilldown.
type TaxonomyNodeRecordsFilters struct {
	TenantID string `form:"tenant_id" validate:"required,no_null_bytes,min=1,max=255"`
	Limit    int    `form:"limit"     validate:"omitempty,min=1,max=100"`
}

// RemoveTaxonomyNodeFilters scopes a taxonomy node soft-remove request.
type RemoveTaxonomyNodeFilters struct {
	TenantID string `form:"tenant_id" validate:"required,no_null_bytes,min=1,max=255"`
	ActorID  string `form:"actor_id"  validate:"required,no_null_bytes,min=1,max=255"`
}

// TaxonomyNodeRecordsResponse contains feedback records for a taxonomy node.
type TaxonomyNodeRecordsResponse struct {
	Data  []FeedbackRecord `json:"data"`
	Limit int              `json:"limit"`
}
