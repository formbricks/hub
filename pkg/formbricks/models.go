package formbricks

import "time"

// ResponsesResponse represents the API response for getting responses
type ResponsesResponse struct {
	Data []Response `json:"data"`
}

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
