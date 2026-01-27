package formbricks

import "time"

// ResponsesResponse represents the API response for getting responses
type ResponsesResponse struct {
	Data []Response `json:"data"`
}

// WebhookEvent represents a webhook event from Formbricks
type WebhookEvent struct {
	WebhookID string   `json:"webhookId"`
	Event     string   `json:"event"` // e.g., "responseCreated", "responseUpdated", "responseFinished"
	Data      Response `json:"data"`  // The response data
}

// Webhook event types
const (
	EventResponseCreated  = "responseCreated"
	EventResponseUpdated  = "responseUpdated"
	EventResponseFinished = "responseFinished"
	EventTestEndpoint     = "testEndpoint" // Test event sent when verifying webhook URL
)

// Response represents a single survey response
type Response struct {
	ID                string                  `json:"id"`
	CreatedAt         time.Time               `json:"createdAt"`
	UpdatedAt         time.Time               `json:"updatedAt"`
	Finished          bool                    `json:"finished"`
	SurveyID          string                  `json:"surveyId"`
	ContactID         *string                 `json:"contactId"`
	EndingID          *string                 `json:"endingId"`
	Data              map[string]interface{}  `json:"data"`
	Variables         map[string]interface{}  `json:"variables"`
	TTC               map[string]interface{}  `json:"ttc"` // Time to complete - can have "_total" and question IDs
	Meta              Meta                    `json:"meta"`
	ContactAttributes *map[string]interface{} `json:"contactAttributes"`
	SingleUseID       *string                 `json:"singleUseId"`
	Language          *string                 `json:"language"`
	DisplayID         string                  `json:"displayId"`
	Survey            *Survey                 `json:"survey,omitempty"` // Included in webhook events
	Tags              []string                `json:"tags,omitempty"`
}

// Survey represents survey metadata (included in webhook events)
type Survey struct {
	Title     string    `json:"title"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SurveyDetails represents the survey structure from the Management API (v1)
type SurveyDetails struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Type      string        `json:"type"`
	Status    string        `json:"status"`
	Questions []Question    `json:"questions"` // Legacy - may be empty
	Blocks    []SurveyBlock `json:"blocks"`    // New structure - questions are in blocks[].elements[]
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

// SurveyBlock represents a block containing survey elements
type SurveyBlock struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Elements []Question `json:"elements"`
}

// GetAllQuestions returns all questions, preferring blocks[].elements[] over legacy questions array
// If blocks contain elements, use those. Otherwise fall back to legacy questions array.
func (s *SurveyDetails) GetAllQuestions() []Question {
	// First, try to get questions from blocks (new structure)
	var blockQuestions []Question
	for _, block := range s.Blocks {
		blockQuestions = append(blockQuestions, block.Elements...)
	}

	// If blocks have elements, use those
	if len(blockQuestions) > 0 {
		return blockQuestions
	}

	// Otherwise, fall back to legacy questions array
	return s.Questions
}

// Question represents a question/element in a survey
type Question struct {
	ID        string           `json:"id"`
	Type      string           `json:"type"`
	Headline  LocalizedText    `json:"headline"`
	Subheader LocalizedText    `json:"subheader,omitempty"`
	Required  bool             `json:"required"`
	Choices   []QuestionChoice `json:"choices,omitempty"`
}

// LocalizedText represents text with localization support
// Formbricks uses {"default": "text"} format for localized strings
type LocalizedText struct {
	Default string `json:"default"`
}

// QuestionChoice represents a choice option in a multiple choice question
type QuestionChoice struct {
	ID    string        `json:"id"`
	Label LocalizedText `json:"label"`
}

// Meta contains metadata about the response
type Meta struct {
	URL       string    `json:"url"`
	Country   string    `json:"country"`
	UserAgent UserAgent `json:"userAgent"`
}

// UserAgent contains browser/device information
type UserAgent struct {
	OS      string `json:"os"`
	Device  string `json:"device"`
	Browser string `json:"browser"`
}
