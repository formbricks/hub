package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/formbricks/hub/internal/llm"
	"github.com/formbricks/hub/internal/models"
)

// ErrSentimentResponseInvalid is returned when the provider's structured output cannot be
// parsed or carries an unknown sentiment label. Structured output makes this rare, but the
// worker still treats it as a normal provider failure (retry, then fail) rather than trusting
// an out-of-contract response.
var ErrSentimentResponseInvalid = errors.New("sentiment response invalid")

// SentimentResult is the parsed, validated output of one sentiment classification:
// a known label and a score clamped to [models.SentimentScoreMin, SentimentScoreMax].
type SentimentResult struct {
	Label models.SentimentValue
	Score float64
}

// SentimentClient classifies the polarity of open feedback text into a sentiment label and
// score. Implementations call an LLM provider (OpenAI or Google) via structured output; the
// factory selects one from configuration. It mirrors the TranslationClient seam so the worker
// depends on the interface, not a provider.
type SentimentClient interface {
	// Classify returns the sentiment of text. sourceLang is the record's BCP-47 language and
	// may be empty; it is passed to the model only as a hint (classification is multilingual).
	Classify(ctx context.Context, text, sourceLang string) (SentimentResult, error)
}

// rawStructuredCompleter is the low-level provider call (system prompt + user text + schema ->
// JSON text), satisfied by *openai.Client and *googleai.Client. It mirrors rawTranslator.
type rawStructuredCompleter interface {
	CompleteJSON(ctx context.Context, systemPrompt, userText string, schema llm.Schema) (string, error)
}

// promptSentimentClient adapts a rawStructuredCompleter to SentimentClient by building the
// prompt and schema, then parsing and validating the JSON response. The provider call stays
// prompt-agnostic (the client owns the contract), mirroring promptTranslationClient.
type promptSentimentClient struct {
	raw rawStructuredCompleter
}

// sentimentResponse is the on-the-wire structured-output shape. It is decoded then validated
// into a SentimentResult; the field names match sentimentResponseSchema.
type sentimentResponse struct {
	Sentiment string `json:"sentiment"`
	// Score is a pointer so an omitted field is distinguishable from a real 0 — a response
	// missing score is out of contract and rejected rather than persisted as neutral.
	Score *float64 `json:"score"`
}

// Classify builds the prompt and schema, calls the provider, and parses the result.
func (c promptSentimentClient) Classify(ctx context.Context, text, sourceLang string) (SentimentResult, error) {
	systemPrompt, userText := buildSentimentPrompt(text, sourceLang)

	raw, err := c.raw.CompleteJSON(ctx, systemPrompt, userText, sentimentResponseSchema)
	if err != nil {
		return SentimentResult{}, fmt.Errorf("classify sentiment: %w", err)
	}

	return parseSentimentResult(raw)
}

// sentimentResponseSchema is the structured-output contract: the sentiment label (one of
// models.SentimentValues) and a numeric polarity score. The enum is derived from
// models.SentimentValues so it cannot drift from the Go set / DB CHECK. Numeric bounds are
// enforced after parsing (clampSentimentScore), not in the schema — OpenAI strict mode does
// not support minimum/maximum.
var sentimentResponseSchema = llm.Schema{
	Name: "sentiment",
	Properties: []llm.Property{
		{
			Name:        "sentiment",
			Type:        llm.TypeString,
			Description: "Overall sentiment polarity of the feedback text.",
			Enum:        sentimentLabelStrings(),
		},
		{
			Name: "score",
			Type: llm.TypeNumber,
			Description: "Polarity intensity from -2 (very negative) to 2 (very positive). " +
				"Use 0 for neutral or mixed.",
		},
	},
}

// buildSentimentPrompt renders the system prompt and user text. The system prompt fixes the
// label scale and score mapping; the source language, when known, is given only as a hint
// (sentiment is classified directly from the text, in any language).
func buildSentimentPrompt(text, sourceLang string) (systemPrompt, userText string) {
	var builder strings.Builder

	builder.WriteString(
		"You are a sentiment-analysis expert. Classify the overall sentiment of the user's " +
			"feedback on this scale:\n" +
			"- very_negative (score -2)\n" +
			"- negative (score -1)\n" +
			"- neutral (score 0)\n" +
			"- positive (score 1)\n" +
			"- very_positive (score 2)\n" +
			"- mixed (score 0): clearly expresses both strong positive and strong negative sentiment\n\n" +
			"Return the single best-fitting label and a score from -2 to 2 reflecting intensity; " +
			"use 0 for neutral or mixed. When unsure, default to neutral (0).",
	)

	if hint := languageDisplayName(sourceLang); hint != "" {
		builder.WriteString(" The feedback is written in ")
		builder.WriteString(hint)
		builder.WriteString(".")
	}

	return builder.String(), text
}

// parseSentimentResult decodes and validates the provider's JSON: the label must be a known
// SentimentValue and the score is clamped to the persisted range. An unparseable response or
// unknown label is ErrSentimentResponseInvalid.
func parseSentimentResult(raw string) (SentimentResult, error) {
	var resp sentimentResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return SentimentResult{}, fmt.Errorf("%w: decode: %w", ErrSentimentResponseInvalid, err)
	}

	label := models.SentimentValue(strings.TrimSpace(resp.Sentiment))
	if !label.IsValid() {
		return SentimentResult{}, fmt.Errorf("%w: unknown label %q", ErrSentimentResponseInvalid, resp.Sentiment)
	}

	if resp.Score == nil {
		return SentimentResult{}, fmt.Errorf("%w: missing score", ErrSentimentResponseInvalid)
	}

	return SentimentResult{Label: label, Score: clampSentimentScore(*resp.Score)}, nil
}

// clampSentimentScore bounds a score to [models.SentimentScoreMin, SentimentScoreMax] so a
// model returning a slightly out-of-range value still satisfies the DB CHECK.
func clampSentimentScore(score float64) float64 {
	switch {
	case score < models.SentimentScoreMin:
		return models.SentimentScoreMin
	case score > models.SentimentScoreMax:
		return models.SentimentScoreMax
	default:
		return score
	}
}

// sentimentLabelStrings returns the valid sentiment labels as strings, in models.SentimentValues
// order, for the structured-output enum.
func sentimentLabelStrings() []string {
	values := models.SentimentValues()
	labels := make([]string, len(values))

	for i, value := range values {
		labels[i] = string(value)
	}

	return labels
}
