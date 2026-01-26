package formbricks

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/formbricks/hub/internal/connector"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/formbricks"
)

// Connector represents a Formbricks polling connector
type Connector struct {
	client          *formbricks.Client
	surveyID        string
	feedbackService *service.FeedbackRecordsService
}

// Config holds configuration for the Formbricks connector
type Config struct {
	URL             string
	APIKey          string
	SurveyID        string
	FeedbackService *service.FeedbackRecordsService
}

// NewConnector creates a new Formbricks connector
func NewConnector(cfg Config) *Connector {
	client := formbricks.NewClientWithBaseURL(cfg.URL, cfg.APIKey)

	return &Connector{
		client:          client,
		surveyID:        cfg.SurveyID,
		feedbackService: cfg.FeedbackService,
	}
}

// Poll fetches responses from Formbricks and creates feedback records
// Implements connector.PullInputConnector interface
func (c *Connector) Poll(ctx context.Context) error {
	slog.Info("Polling Formbricks for responses", "survey_id", c.surveyID)

	// Get responses from Formbricks
	responses, err := c.client.GetResponses(formbricks.GetResponsesOptions{
		SurveyID: c.surveyID,
	})
	if err != nil {
		slog.Error("Failed to get responses from Formbricks", "error", err, "survey_id", c.surveyID)
		return err
	}

	slog.Info("Retrieved responses from Formbricks", "count", len(responses.Data))

	// Process each response
	for _, response := range responses.Data {
		if !response.Finished {
			slog.Debug("Skipping unfinished response", "response_id", response.ID)
			continue
		}

		// Transform Formbricks response to feedback records
		feedbackRecords := TransformResponseToFeedbackRecords(response)

		// Create feedback records using the service
		for _, recordReq := range feedbackRecords {
			record, err := c.feedbackService.CreateFeedbackRecord(ctx, recordReq)
			if err != nil {
				slog.Error("Failed to create feedback record",
					"error", err,
					"response_id", response.ID,
					"field_id", recordReq.FieldID,
				)
				continue
			}
			slog.Info("Created feedback record",
				"record_id", record.ID,
				"response_id", response.ID,
				"field_id", recordReq.FieldID,
			)
		}
	}

	return nil
}

// StartIfConfigured starts the Formbricks connector if environment variables are configured
func StartIfConfigured(ctx context.Context, feedbackService *service.FeedbackRecordsService) {
	formbricksURL := os.Getenv("FORMBRICKS_URL")
	if formbricksURL == "" {
		formbricksURL = "https://app.formbricks.com/api/v2" // Default
	}

	formbricksKey := os.Getenv("FORMBRICKS_API_KEY")
	surveyID := os.Getenv("FORMBRICKS_SURVEY_ID")

	// Only start connector if both API key and survey ID are provided
	if formbricksKey == "" || surveyID == "" {
		slog.Info("Formbricks connector not configured (FORMBRICKS_API_KEY and FORMBRICKS_SURVEY_ID required)")
		return
	}

	pollInterval := 1 * time.Hour // Default poll interval

	fbConnector := NewConnector(Config{
		URL:             formbricksURL,
		APIKey:          formbricksKey,
		SurveyID:        surveyID,
		FeedbackService: feedbackService,
	})

	// Use the generic poller to manage polling
	poller := connector.NewPoller(pollInterval, "formbricks")
	poller.Start(ctx, fbConnector)
}
