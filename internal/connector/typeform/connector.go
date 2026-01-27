package typeform

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/formbricks/hub/internal/connector"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/typeform"
)

// Connector represents a Typeform polling connector
type Connector struct {
	client          *typeform.Client
	formID          string
	formTitle       string
	feedbackService *service.FeedbackRecordsService

	// Cached field labels (fieldID/ref -> title)
	fieldLabels     map[string]string
	fieldLabelsMu   sync.RWMutex
	fieldLabelsFetched bool
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
		fieldLabels:     make(map[string]string),
	}
}

// fetchFormDetails fetches form structure to get field labels (called once on first poll)
func (c *Connector) fetchFormDetails() error {
	c.fieldLabelsMu.Lock()
	defer c.fieldLabelsMu.Unlock()

	// Already fetched
	if c.fieldLabelsFetched {
		return nil
	}

	slog.Info("Fetching Typeform form details", "form_id", c.formID)

	form, err := c.client.GetForm(c.formID)
	if err != nil {
		slog.Error("Failed to fetch form details from Typeform", "error", err, "form_id", c.formID)
		return err
	}

	// Store form title
	c.formTitle = form.Title

	// Build field labels map (both by ID and by ref for lookup)
	for _, field := range form.Fields {
		// Map by field ID
		c.fieldLabels[field.ID] = field.Title

		// Also map by ref if available (responses may use ref instead of ID)
		if field.Ref != "" {
			c.fieldLabels[field.Ref] = field.Title
		}
	}

	c.fieldLabelsFetched = true
	slog.Info("Cached Typeform field labels",
		"form_id", c.formID,
		"form_title", c.formTitle,
		"fields_count", len(form.Fields),
	)

	return nil
}

// getFieldLabels returns the cached field labels map
func (c *Connector) getFieldLabels() map[string]string {
	c.fieldLabelsMu.RLock()
	defer c.fieldLabelsMu.RUnlock()
	return c.fieldLabels
}

// getFormTitle returns the cached form title
func (c *Connector) getFormTitle() string {
	c.fieldLabelsMu.RLock()
	defer c.fieldLabelsMu.RUnlock()
	return c.formTitle
}

// Poll fetches responses from Typeform and creates feedback records
// Implements connector.PollingInputConnector interface
func (c *Connector) Poll(ctx context.Context) error {
	slog.Info("Polling Typeform for responses", "form_id", c.formID)

	// Fetch form details on first poll to get field labels
	if err := c.fetchFormDetails(); err != nil {
		// Log error but continue - we can still process responses without labels
		slog.Warn("Continuing without field labels", "error", err)
	}

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

	// Get cached data
	fieldLabels := c.getFieldLabels()
	formTitle := c.getFormTitle()

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

		// Transform Typeform response to feedback records with field labels
		feedbackRecords := TransformResponseToFeedbackRecords(response, c.formID, formTitle, fieldLabels)

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
