package hub

import (
	"context"
	"encoding/json"
	"log/slog"
)

// CreateFeedbackRecordRequest represents the request to create a feedback record
// This is a copy of the model for the connector service to use
// TODO: Move to shared SDK package when Hub SDK is created
type CreateFeedbackRecordRequest struct {
	CollectedAt    *string         `json:"collected_at,omitempty"`
	SourceType     string          `json:"source_type"`
	SourceID       *string         `json:"source_id,omitempty"`
	SourceName     *string         `json:"source_name,omitempty"`
	FieldID        string          `json:"field_id"`
	FieldLabel     *string         `json:"field_label,omitempty"`
	FieldType      string          `json:"field_type"`
	ValueText      *string         `json:"value_text,omitempty"`
	ValueNumber    *float64        `json:"value_number,omitempty"`
	ValueBoolean   *bool           `json:"value_boolean,omitempty"`
	ValueDate      *string         `json:"value_date,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Language       *string         `json:"language,omitempty"`
	UserIdentifier *string         `json:"user_identifier,omitempty"`
	TenantID       *string         `json:"tenant_id,omitempty"`
	ResponseID     *string         `json:"response_id,omitempty"`
}

// Client represents a client for interacting with the Hub API
// This is a placeholder implementation for the connector service
// TODO: Move to connector-service/internal/hub/client.go when connector service is created
type Client struct {
	baseURL string
	apiKey  string
}

// NewClient creates a new Hub API client
// This is a placeholder implementation - will be replaced with actual SDK
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
	}
}

// CreateFeedbackRecord creates a feedback record in Hub
// Placeholder implementation: logs the record instead of making HTTP call
// TODO: Replace with actual HTTP call to POST /v1/feedback-records when SDK is ready
func (c *Client) CreateFeedbackRecord(ctx context.Context, record *CreateFeedbackRecordRequest) error {
	slog.Info("HubCreateFeedbackRecord (placeholder)",
		"record", record,
		"base_url", c.baseURL,
	)
	// Placeholder: return nil (no error)
	// TODO: Make actual HTTP call to Hub API
	return nil
}

// CreateFeedbackRecords creates multiple feedback records in Hub (batch)
// Placeholder implementation: logs all records
// TODO: Replace with actual HTTP call when SDK is ready
func (c *Client) CreateFeedbackRecords(ctx context.Context, records []*CreateFeedbackRecordRequest) error {
	slog.Info("HubCreateFeedbackRecords (placeholder)",
		"count", len(records),
		"base_url", c.baseURL,
	)
	// Placeholder: return nil (no error)
	// TODO: Make actual HTTP call to Hub API
	return nil
}
