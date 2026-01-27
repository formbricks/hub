package typeform

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/formbricks/hub/internal/connector"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/typeform"
)

// Connector represents a Typeform polling connector
type Connector struct {
	client          *typeform.Client
	formID          string
	feedbackService *service.FeedbackRecordsService
}

// Config holds configuration for the Typeform connector
type Config struct {
	URL             string
	AccessToken     string
	FormID          string
	FeedbackService *service.FeedbackRecordsService
}

// NewConnector creates a new Typeform connector
func NewConnector(cfg Config) *Connector {
	client := typeform.NewClientWithBaseURL(cfg.URL, cfg.AccessToken)

	return &Connector{
		client:          client,
		formID:          cfg.FormID,
		feedbackService: cfg.FeedbackService,
	}
}

// Poll fetches responses from Typeform and creates feedback records
// Implements connector.PollingInputConnector interface
func (c *Connector) Poll(ctx context.Context) error {
	slog.Info("Polling Typeform for responses", "form_id", c.formID)

	// Get only completed responses from Typeform
	completed := true
	responses, err := c.client.GetResponses(typeform.GetResponsesOptions{
		FormID:    c.formID,
		Completed: &completed,
		PageSize:  100, // Reasonable page size
	})
	if err != nil {
		slog.Error("Failed to get responses from Typeform", "error", err, "form_id", c.formID)
		return err
	}

	slog.Info("Retrieved responses from Typeform", "count", len(responses.Items), "total", responses.TotalItems)

	// Process each response
	for _, response := range responses.Items {
		// Skip responses that weren't actually submitted
		if !response.IsSubmitted() {
			slog.Debug("Skipping unsubmitted response", "response_id", response.GetID())
			continue
		}

		// Skip responses with no answers
		if len(response.Answers) == 0 {
			slog.Debug("Skipping response with no answers", "response_id", response.GetID())
			continue
		}

		// Transform Typeform response to feedback records
		feedbackRecords := TransformResponseToFeedbackRecords(response, c.formID)

		// Create feedback records using the service
		for _, recordReq := range feedbackRecords {
			record, err := c.feedbackService.CreateFeedbackRecord(ctx, recordReq)
			if err != nil {
				slog.Error("Failed to create feedback record",
					"error", err,
					"response_id", response.GetID(),
					"field_id", recordReq.FieldID,
				)
				continue
			}
			slog.Info("Created feedback record",
				"record_id", record.ID,
				"response_id", response.GetID(),
				"field_id", recordReq.FieldID,
			)
		}
	}

	return nil
}

// StartIfConfigured starts the Typeform connector if environment variables are configured
func StartIfConfigured(ctx context.Context, feedbackService *service.FeedbackRecordsService) {
	accessToken := os.Getenv("TYPEFORM_POLLING_API_KEY")
	formID := os.Getenv("TYPEFORM_FORM_ID")

	// Only start connector if both access token and form ID are provided
	if accessToken == "" || formID == "" {
		slog.Info("Typeform polling connector not configured (TYPEFORM_POLLING_API_KEY and TYPEFORM_FORM_ID required)")
		return
	}

	pollInterval := 1 * time.Hour // Default poll interval

	// Typeform is SaaS-only, URL is always https://api.typeform.com
	tfConnector := NewConnector(Config{
		URL:             "https://api.typeform.com",
		AccessToken:     accessToken,
		FormID:          formID,
		FeedbackService: feedbackService,
	})

	// Use the generic poller to manage polling
	poller := connector.NewPoller(pollInterval, "typeform")
	poller.Start(ctx, tfConnector)
}
