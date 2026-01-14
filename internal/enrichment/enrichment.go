// Package enrichment provides AI-powered text analysis using OpenAI's API.
// It extracts sentiment, emotion, and topics from open-ended text feedback.
// All operations are designed to be called asynchronously by background workers.
package enrichment

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

const (
	// maxTextLength is the maximum text length before truncation (1000 chars ≈ 250 tokens)
	maxTextLength = 1000
	// maxTopics is the maximum number of topics to return
	maxTopics = 5
	// defaultTemperature is the default temperature for OpenAI models that support it
	defaultTemperature = 0.0
)

// Enrichment holds the structured AI analysis results
type Enrichment struct {
	Sentiment      string   `json:"sentiment"`       // positive, negative, neutral
	SentimentScore float64  `json:"sentiment_score"` // -1 to +1
	Emotion        string   `json:"emotion"`         // joy, anger, frustration, sadness, neutral
	Topics         []string `json:"topics"`          // key themes
}

// Service handles AI-powered text enrichment
type Service struct {
	client  openai.Client
	model   string
	timeout time.Duration
	logger  *slog.Logger
}

// NewService creates a new enrichment service
func NewService(apiKey string, model string, timeoutSeconds int, logger *slog.Logger) *Service {
	return &Service{
		client:  openai.NewClient(option.WithAPIKey(apiKey)),
		model:   model,
		timeout: time.Duration(timeoutSeconds) * time.Second,
		logger:  logger,
	}
}

// EnrichText analyzes text and extracts structured insights
func (s *Service) EnrichText(ctx context.Context, text string) (*Enrichment, error) {
	// Apply timeout
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	prompt := s.buildPrompt(text)

	params := openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfString: openai.String(prompt),
					},
				},
			},
		},
		Model: shared.ChatModel(s.model),
	}

	// Only set temperature for models that support it (gpt-5-mini requires default temperature=1)
	if s.model != "gpt-5-mini" {
		params.Temperature = openai.Float(defaultTemperature)
	}

	resp, err := s.client.Chat.Completions.New(ctx, params)

	if err != nil {
		return nil, fmt.Errorf("openai api error: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from openai")
	}

	content := resp.Choices[0].Message.Content

	var enrichment Enrichment
	if err := json.Unmarshal([]byte(content), &enrichment); err != nil {
		s.logger.Warn("failed to parse enrichment response", "error", err, "content", content)
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Validate and normalize
	enrichment = s.normalizeEnrichment(enrichment)

	return &enrichment, nil
}

// buildPrompt creates the LLM prompt for text analysis
func (s *Service) buildPrompt(text string) string {
	// Truncate very long text to avoid token limits
	if len(text) > maxTextLength {
		text = text[:maxTextLength] + "..."
	}

	return fmt.Sprintf(`You are a feedback analysis assistant. Analyze the following feedback and output JSON with these exact keys:

{
  "sentiment": "positive" | "negative" | "neutral",
  "sentiment_score": number between -1.0 (very negative) and 1.0 (very positive),
  "emotion": "joy" | "anger" | "frustration" | "sadness" | "neutral",
  "topics": array of 2-4 short topic keywords (e.g., ["pricing", "UI", "performance"])
}

Rules:
- Output ONLY valid JSON, no additional text
- Use lowercase for sentiment and emotion
- Topics should be concise keywords, not full sentences
- If unclear, default to "neutral" sentiment and 0.0 score
- If a question is provided, use it as context for topic extraction

Feedback:
"%s"`, text)
}

// normalizeEnrichment validates and normalizes the enrichment data
func (s *Service) normalizeEnrichment(e Enrichment) Enrichment {
	// Normalize sentiment
	switch e.Sentiment {
	case "positive", "negative", "neutral":
		// valid
	default:
		e.Sentiment = "neutral"
	}

	// Clamp sentiment score
	if e.SentimentScore < -1.0 {
		e.SentimentScore = -1.0
	} else if e.SentimentScore > 1.0 {
		e.SentimentScore = 1.0
	}

	// Normalize emotion
	validEmotions := map[string]bool{
		"joy": true, "anger": true, "frustration": true,
		"sadness": true, "neutral": true,
	}
	if !validEmotions[e.Emotion] {
		e.Emotion = "neutral"
	}

	// Limit topics to maximum allowed
	if len(e.Topics) > maxTopics {
		e.Topics = e.Topics[:maxTopics]
	}

	return e
}

// Model returns the model name being used
func (s *Service) Model() string {
	return s.model
}
