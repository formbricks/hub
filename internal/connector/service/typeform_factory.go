package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/hub"
	"github.com/formbricks/hub/pkg/typeform"
)

// TypeformFactory creates Typeform connectors
type TypeformFactory struct{}

// GetName returns the connector name
func (f *TypeformFactory) GetName() string {
	return "typeform"
}

// CreateConnector creates a Typeform connector from instance configuration
func (f *TypeformFactory) CreateConnector(ctx context.Context, instance *models.ConnectorInstance, hubClient *hub.Client) (PollingConnector, error) {
	configMap, err := ParseConfig(instance.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Extract required config fields
	accessToken, ok := configMap["access_token"].(string)
	if !ok || accessToken == "" {
		return nil, fmt.Errorf("access_token is required in config")
	}

	formID, ok := configMap["form_id"].(string)
	if !ok || formID == "" {
		return nil, fmt.Errorf("form_id is required in config")
	}

	// Typeform is SaaS-only, URL is always https://api.typeform.com
	client := typeform.NewClient(accessToken)

	connector := &TypeformConnector{
		client:     client,
		formID:     formID,
		instanceID: instance.ID.String(),
		hubClient:  hubClient,
	}

	slog.Info("Created Typeform connector",
		"instance_id", instance.ID,
		"form_id", formID,
	)

	return connector, nil
}

// TypeformConnector implements PollingConnector for Typeform
type TypeformConnector struct {
	client     *typeform.Client
	formID     string
	instanceID string
	hubClient  *hub.Client
	lastID     string
}

// Poll fetches responses from Typeform and sends them to Hub
func (c *TypeformConnector) Poll(ctx context.Context) error {
	slog.Info("Polling Typeform for responses",
		"instance_id", c.instanceID,
		"form_id", c.formID,
	)

	// Get only completed responses from Typeform
	completed := true
	responses, err := c.client.GetResponses(typeform.GetResponsesOptions{
		FormID:    c.formID,
		Completed: &completed,
		PageSize:  100,
	})
	if err != nil {
		return fmt.Errorf("failed to get responses: %w", err)
	}

	slog.Info("Retrieved responses from Typeform",
		"instance_id", c.instanceID,
		"count", len(responses.Items),
	)

	// Transform and send to Hub
	for _, response := range responses.Items {
		if !response.IsSubmitted() {
			continue
		}

		// TODO: Transform response to feedback records
		// For now, create a simple placeholder record
		record := &hub.CreateFeedbackRecordRequest{
			SourceType: "typeform",
			SourceID:   &c.formID,
			FieldID:    "response",
			FieldType:  "text",
		}

		if err := c.hubClient.CreateFeedbackRecord(ctx, record); err != nil {
			slog.Error("Failed to create feedback record",
				"instance_id", c.instanceID,
				"error", err,
			)
			continue
		}

		// Track last ID
		if responseID := response.GetID(); responseID != "" {
			c.lastID = responseID
		}
	}

	return nil
}

// ExtractLastID returns the last processed ID for pagination
func (c *TypeformConnector) ExtractLastID() (string, error) {
	return c.lastID, nil
}
