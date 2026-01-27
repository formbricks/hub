package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/formbricks"
	"github.com/formbricks/hub/pkg/hub"
)

// FormbricksFactory creates Formbricks connectors
type FormbricksFactory struct{}

// GetName returns the connector name
func (f *FormbricksFactory) GetName() string {
	return "formbricks"
}

// CreateConnector creates a Formbricks connector from instance configuration
func (f *FormbricksFactory) CreateConnector(ctx context.Context, instance *models.ConnectorInstance, hubClient *hub.Client) (PollingConnector, error) {
	configMap, err := ParseConfig(instance.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Extract required config fields
	baseURL, ok := configMap["base_url"].(string)
	if !ok || baseURL == "" {
		baseURL = "https://app.formbricks.com" // Default
	}

	apiKey, ok := configMap["api_key"].(string)
	if !ok || apiKey == "" {
		return nil, fmt.Errorf("api_key is required in config")
	}

	surveyID, ok := configMap["survey_id"].(string)
	if !ok || surveyID == "" {
		return nil, fmt.Errorf("survey_id is required in config")
	}

	client := formbricks.NewClientWithBaseURL(baseURL, apiKey)

	connector := &FormbricksConnector{
		client:     client,
		surveyID:   surveyID,
		instanceID: instance.ID.String(),
		hubClient:  hubClient,
	}

	slog.Info("Created Formbricks connector",
		"instance_id", instance.ID,
		"survey_id", surveyID,
	)

	return connector, nil
}

// FormbricksConnector implements PollingConnector for Formbricks
type FormbricksConnector struct {
	client     *formbricks.Client
	surveyID   string
	instanceID string
	hubClient  *hub.Client
	lastID     string
}

// Poll fetches responses from Formbricks and sends them to Hub
func (c *FormbricksConnector) Poll(ctx context.Context) error {
	slog.Info("Polling Formbricks for responses",
		"instance_id", c.instanceID,
		"survey_id", c.surveyID,
	)

	// Get responses from Formbricks
	// TODO: Use last_id for pagination
	responses, err := c.client.GetResponses(formbricks.GetResponsesOptions{
		SurveyID: c.surveyID,
	})
	if err != nil {
		return fmt.Errorf("failed to get responses: %w", err)
	}

	slog.Info("Retrieved responses from Formbricks",
		"instance_id", c.instanceID,
		"count", len(responses.Data),
	)

	// Transform and send to Hub
	for _, response := range responses.Data {
		if !response.Finished {
			continue
		}

		// TODO: Transform response to feedback records
		// For now, create a simple placeholder record
		record := &hub.CreateFeedbackRecordRequest{
			SourceType: "formbricks",
			SourceID:   &c.surveyID,
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
		if response.ID != "" {
			c.lastID = response.ID
		}
	}

	return nil
}

// ExtractLastID returns the last processed ID for pagination
func (c *FormbricksConnector) ExtractLastID() (string, error) {
	return c.lastID, nil
}
