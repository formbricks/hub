package typeform

import "time"

// Form represents a Typeform form definition
type Form struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Language string      `json:"language"`
	Fields   []FormField `json:"fields"`
}

// FormField represents a field/question in a form
type FormField struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Ref        string            `json:"ref,omitempty"`
	Type       string            `json:"type"`
	Properties FormFieldProperties `json:"properties,omitempty"`
}

// FormFieldProperties contains additional field properties
type FormFieldProperties struct {
	Description string `json:"description,omitempty"`
}

// ResponsesResponse represents the API response for getting responses
type ResponsesResponse struct {
	TotalItems int        `json:"total_items"`
	PageCount  int        `json:"page_count"`
	Items      []Response `json:"items"`
}

// Response represents a single form response
type Response struct {
	LandingID   string                 `json:"landing_id"`
	Token       string                 `json:"token"`
	ResponseID  string                 `json:"response_id,omitempty"`
	LandedAt    time.Time              `json:"landed_at"`
	SubmittedAt time.Time              `json:"submitted_at"`
	Metadata    Metadata               `json:"metadata"`
	Answers     []Answer               `json:"answers"`
	Hidden      map[string]interface{} `json:"hidden"`
	Calculated  Calculated             `json:"calculated"`
	Variables   []Variable             `json:"variables"`
}

// IsSubmitted checks if the response was actually submitted (not just a landing)
func (r *Response) IsSubmitted() bool {
	// Typeform returns "0001-01-01T00:00:00Z" for unsubmitted responses
	return !r.SubmittedAt.IsZero() && r.SubmittedAt.Year() > 1
}

// GetID returns the response ID (token is the primary identifier)
func (r *Response) GetID() string {
	if r.ResponseID != "" {
		return r.ResponseID
	}
	return r.Token
}

// Metadata contains information about the respondent's environment
type Metadata struct {
	UserAgent string `json:"user_agent"`
	Platform  string `json:"platform"`
	Referer   string `json:"referer"`
	NetworkID string `json:"network_id"`
	Browser   string `json:"browser"`
}

// Answer represents a single answer to a question
type Answer struct {
	Field       Field       `json:"field"`
	Type        string      `json:"type"` // text, number, boolean, choice, choices, email, url, file_url, date, payment, phone_number, multi_format
	Text        string      `json:"text,omitempty"`
	Number      *float64    `json:"number,omitempty"`
	Boolean     *bool       `json:"boolean,omitempty"`
	Email       string      `json:"email,omitempty"`
	URL         string      `json:"url,omitempty"`
	FileURL     string      `json:"file_url,omitempty"`
	Date        *time.Time  `json:"date,omitempty"`
	Choice      *Choice     `json:"choice,omitempty"`
	Choices     *Choices    `json:"choices,omitempty"`
	Payment     *Payment    `json:"payment,omitempty"`
	PhoneNumber string      `json:"phone_number,omitempty"`
	MultiFormat *MultiFormat `json:"multi_format,omitempty"`
}

// Field represents the question/field being answered
type Field struct {
	ID   string `json:"id"`
	Type string `json:"type"` // short_text, long_text, dropdown, multiple_choice, email, number, rating, opinion_scale, date, yes_no, legal, file_upload, etc.
	Ref  string `json:"ref,omitempty"`
}

// Choice represents a single choice answer
type Choice struct {
	Label string `json:"label"`
	Other string `json:"other,omitempty"`
}

// Choices represents multiple choice answers
type Choices struct {
	Labels []string `json:"labels"`
	Other  string   `json:"other,omitempty"`
}

// Payment represents a payment answer
type Payment struct {
	Amount    string `json:"amount"`
	Last4     string `json:"last4"`
	Name      string `json:"name"`
	Success   bool   `json:"success"`
}

// MultiFormat represents audio/video answers
type MultiFormat struct {
	AudioURL        string `json:"audio_url,omitempty"`
	AudioTranscript string `json:"audio_transcript,omitempty"`
	VideoURL        string `json:"video_url,omitempty"`
	VideoTranscript string `json:"video_transcript,omitempty"`
}

// Calculated contains calculated values like score
type Calculated struct {
	Score int `json:"score"`
}

// Variable represents a form variable
type Variable struct {
	Key    string   `json:"key"`
	Type   string   `json:"type"` // number, text
	Number *float64 `json:"number,omitempty"`
	Text   string   `json:"text,omitempty"`
}

// Question types
const (
	QuestionTypeShortText      = "short_text"
	QuestionTypeLongText       = "long_text"
	QuestionTypeDropdown       = "dropdown"
	QuestionTypeMultipleChoice = "multiple_choice"
	QuestionTypePictureChoice  = "picture_choice"
	QuestionTypeEmail          = "email"
	QuestionTypeWebsite        = "website"
	QuestionTypeFileUpload     = "file_upload"
	QuestionTypeMultiFormat    = "multi_format"
	QuestionTypeLegal          = "legal"
	QuestionTypeYesNo          = "yes_no"
	QuestionTypeRating         = "rating"
	QuestionTypeOpinionScale   = "opinion_scale"
	QuestionTypeNumber         = "number"
	QuestionTypeDate           = "date"
	QuestionTypePayment        = "payment"
	QuestionTypePhoneNumber    = "phone_number"
)

// Answer types
const (
	AnswerTypeText        = "text"
	AnswerTypeNumber      = "number"
	AnswerTypeBoolean     = "boolean"
	AnswerTypeChoice      = "choice"
	AnswerTypeChoices     = "choices"
	AnswerTypeEmail       = "email"
	AnswerTypeURL         = "url"
	AnswerTypeFileURL     = "file_url"
	AnswerTypeDate        = "date"
	AnswerTypePayment     = "payment"
	AnswerTypePhoneNumber = "phone_number"
	AnswerTypeMultiFormat = "multi_format"
)
