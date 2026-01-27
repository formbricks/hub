package formbricks

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/formbricks/hub/internal/connector"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/formbricks"
)

// Connector represents a Formbricks polling connector
type Connector struct {
	client          *formbricks.Client
	surveyID        string
	surveyName      string
	feedbackService *service.FeedbackRecordsService

	// Cached field labels (questionID -> headline)
	fieldLabels        map[string]string
	fieldLabelsMu      sync.RWMutex
	fieldLabelsFetched bool
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
		fieldLabels:     make(map[string]string),
	}
}

// fetchSurveyDetails fetches survey structure to get question labels (called once on first poll)
func (c *Connector) fetchSurveyDetails() error {
	c.fieldLabelsMu.Lock()
	defer c.fieldLabelsMu.Unlock()

	// Already fetched
	if c.fieldLabelsFetched {
		return nil
	}

	slog.Info("Fetching Formbricks survey details", "survey_id", c.surveyID)

	survey, err := c.client.GetSurvey(c.surveyID)
	if err != nil {
		slog.Error("Failed to fetch survey details from Formbricks", "error", err, "survey_id", c.surveyID)
		return err
	}

	// Store survey name
	c.surveyName = survey.Name

	// Build field labels map (questionID -> headline)
	// Questions can be in legacy "questions" array or in "blocks[].elements[]"
	allQuestions := survey.GetAllQuestions()
	for _, question := range allQuestions {
		c.fieldLabels[question.ID] = question.Headline.Default
	}

	c.fieldLabelsFetched = true
	slog.Info("Cached Formbricks field labels",
		"survey_id", c.surveyID,
		"survey_name", c.surveyName,
		"questions_count", len(allQuestions),
	)

	return nil
}

// getFieldLabels returns the cached field labels map
func (c *Connector) getFieldLabels() map[string]string {
	c.fieldLabelsMu.RLock()
	defer c.fieldLabelsMu.RUnlock()
	return c.fieldLabels
}

// getSurveyName returns the cached survey name
func (c *Connector) getSurveyName() string {
	c.fieldLabelsMu.RLock()
	defer c.fieldLabelsMu.RUnlock()
	return c.surveyName
}

// Poll fetches responses from Formbricks and creates feedback records
// Implements connector.PollingInputConnector interface
func (c *Connector) Poll(ctx context.Context) error {
	slog.Info("Polling Formbricks for responses", "survey_id", c.surveyID)

	// Fetch survey details on first poll to get question labels
	if err := c.fetchSurveyDetails(); err != nil {
		// Log error but continue - we can still process responses without labels
		slog.Warn("Continuing without field labels", "error", err)
	}

	// Get responses from Formbricks
	responses, err := c.client.GetResponses(formbricks.GetResponsesOptions{
		SurveyID: c.surveyID,
	})
	if err != nil {
		slog.Error("Failed to get responses from Formbricks", "error", err, "survey_id", c.surveyID)
		return err
	}

	slog.Info("Retrieved responses from Formbricks", "count", len(responses.Data))

	// Get cached data
	fieldLabels := c.getFieldLabels()
	surveyName := c.getSurveyName()

	// Process each response
	for _, response := range responses.Data {
		if !response.Finished {
			slog.Debug("Skipping unfinished response", "response_id", response.ID)
			continue
		}

		// Transform Formbricks response to feedback records with field labels
		feedbackRecords := TransformResponseToFeedbackRecords(response, surveyName, fieldLabels)

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
	formbricksBaseURL := os.Getenv("FORMBRICKS_BASE_URL")
	if formbricksBaseURL == "" {
		formbricksBaseURL = "https://app.formbricks.com" // Default
	}

	formbricksKey := os.Getenv("FORMBRICKS_POLLING_API_KEY")
	surveyID := os.Getenv("FORMBRICKS_SURVEY_ID")

	// Only start connector if both API key and survey ID are provided
	if formbricksKey == "" || surveyID == "" {
		slog.Info("Formbricks polling connector not configured (FORMBRICKS_POLLING_API_KEY and FORMBRICKS_SURVEY_ID required)")
		return
	}

	pollInterval := 1 * time.Hour // Default poll interval

	fbConnector := NewConnector(Config{
		URL:             formbricksBaseURL,
		APIKey:          formbricksKey,
		SurveyID:        surveyID,
		FeedbackService: feedbackService,
	})

	// Use the generic poller to manage polling
	poller := connector.NewPoller(pollInterval, "formbricks")
	poller.Start(ctx, fbConnector)
}
