package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ConnectorInstance represents a connector instance
type ConnectorInstance struct {
	ID         uuid.UUID       `json:"id"`
	Name       string          `json:"name"`
	InstanceID string          `json:"instance_id"`
	Type       string          `json:"type"`
	Config     json.RawMessage `json:"config"`
	State      json.RawMessage `json:"state,omitempty"`
	Running    bool            `json:"running"`
	Error      *string         `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// ConnectorConfig represents the structure of the config JSONB field
type ConnectorConfig struct {
	PollInterval string `json:"poll_interval,omitempty"`
	// Other connector-specific fields will be added as needed
}

// CreateConnectorInstanceRequest represents the request to create a connector instance
type CreateConnectorInstanceRequest struct {
	Name       string          `json:"name" validate:"required,no_null_bytes,min=1,max=255"`
	InstanceID string          `json:"instance_id" validate:"required,no_null_bytes,min=1,max=255"`
	Type       string          `json:"type" validate:"required,oneof=polling webhook output enrichment"`
	Config     json.RawMessage `json:"config" validate:"required"`
	Running    *bool           `json:"running,omitempty"`
}

// UpdateConnectorInstanceRequest represents the request to update a connector instance
type UpdateConnectorInstanceRequest struct {
	Config  *json.RawMessage `json:"config,omitempty"`
	State   *json.RawMessage `json:"state,omitempty"`
	Running *bool            `json:"running,omitempty"`
	Error   *string          `json:"error,omitempty"`
}

// ListConnectorInstancesFilters represents filters for listing connector instances
type ListConnectorInstancesFilters struct {
	Type    *string `form:"type" validate:"omitempty,oneof=polling webhook output enrichment"`
	Running *bool   `form:"running"`
	Name    *string `form:"name" validate:"omitempty,no_null_bytes"`
	Limit   int     `form:"limit" validate:"omitempty,min=1,max=1000"`
	Offset  int     `form:"offset" validate:"omitempty,min=0"`
}

// ListConnectorInstancesResponse represents the response for listing connector instances
type ListConnectorInstancesResponse struct {
	Data   []ConnectorInstance `json:"data"`
	Total  int64               `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}
