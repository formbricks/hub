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

// ErrEmotionsResponseInvalid is returned when the provider's structured output cannot be parsed.
// The array-of-enum schema keeps this rare; the worker treats it as a normal provider failure
// (retry, then fail). Unknown labels are dropped rather than failing the whole classification, so
// this fires only on a decode error, not on out-of-pool labels.
var ErrEmotionsResponseInvalid = errors.New("emotions response invalid")

// EmotionsResult is the parsed, validated output of one emotion classification: the distinct,
// known emotion labels the text expresses (possibly empty when none apply).
type EmotionsResult struct {
	Labels []models.EmotionValue
}

// EmotionsClient classifies open feedback text into zero or more emotion labels. Implementations
// call an LLM provider (OpenAI or Google) via structured output; the factory selects one from
// configuration. It mirrors the SentimentClient seam so the worker depends on the interface, not a
// provider.
type EmotionsClient interface {
	// Classify returns the emotions expressed in text. sourceLang is the record's BCP-47 language
	// and may be empty; it is passed to the model only as a hint (classification is multilingual).
	Classify(ctx context.Context, text, sourceLang string) (EmotionsResult, error)
}

// promptEmotionsClient adapts a rawStructuredCompleter (the seam shared with sentiment) to
// EmotionsClient by building the prompt and schema, then parsing and validating the JSON response.
type promptEmotionsClient struct {
	raw rawStructuredCompleter
}

// emotionsResponse is the on-the-wire structured-output shape, decoded then normalized into an
// EmotionsResult; the field name matches emotionsResponseSchema.
type emotionsResponse struct {
	Emotions []string `json:"emotions"`
}

// Classify builds the emotions prompt and schema, calls the provider, and parses the result via
// the shared structured-classification path.
func (c promptEmotionsClient) Classify(ctx context.Context, text, sourceLang string) (EmotionsResult, error) {
	return classifyStructured(ctx, c.raw, emotionsSpec, text, sourceLang)
}

// emotionsResponseSchema is the structured-output contract: an array whose elements are each one of
// models.EmotionValues (an array-of-enum). The enum is derived from models.EmotionValues so it
// cannot drift from the Go set / DB CHECK. An empty array means no emotion applies.
var emotionsResponseSchema = llm.Schema{
	Name: "emotions",
	Properties: []llm.Property{
		{
			Name:        "emotions",
			Type:        llm.TypeArray,
			Description: "Every basic emotion the feedback clearly expresses; an empty array when none apply.",
			Items: &llm.Property{
				Type: llm.TypeString,
				Enum: labelStrings(models.EmotionValues()),
			},
		},
	},
}

// emotionsSpec is the structured-classification contract for emotions, driving the shared
// classifyStructured path (mirrors sentimentSpec).
var emotionsSpec = structuredSpec[EmotionsResult]{
	Name:        "emotions",
	Schema:      emotionsResponseSchema,
	BuildPrompt: buildEmotionsPrompt,
	Parse:       parseEmotionsResult,
}

// buildEmotionsPrompt renders the system prompt and user text. The system prompt fixes the emotion
// pool and the multi-label instruction; the source language, when known, is given only as a hint
// (emotions are classified directly from the text, in any language).
func buildEmotionsPrompt(text, sourceLang string) (systemPrompt, userText string) {
	var builder strings.Builder

	// Build the emotion list from the same source as the structured-output enum
	// (labelStrings over models.EmotionValues) so the prompt cannot drift from the schema when
	// the pool changes.
	builder.WriteString(
		"You are an emotion-analysis expert. Identify which of these basic emotions the user's " +
			"feedback clearly expresses:\n",
	)

	for _, label := range labelStrings(models.EmotionValues()) {
		builder.WriteString("- " + label + "\n")
	}

	builder.WriteString(
		"\nReturn every emotion that clearly applies — a message may express several at once, or " +
			"none. Include an emotion only when it is genuinely present in the text; return an empty " +
			"list when none clearly apply. Do not infer emotions that are not expressed.",
	)

	if hint := languageDisplayName(sourceLang); hint != "" {
		builder.WriteString(" The feedback is written in ")
		builder.WriteString(hint)
		builder.WriteString(".")
	}

	return builder.String(), text
}

// parseEmotionsResult decodes the provider's JSON and normalizes it: unknown labels are dropped
// (defense-in-depth behind the array-of-enum schema) and duplicates removed, preserving first-seen
// order. An empty result is valid and clears the column. Only a decode failure is an error.
func parseEmotionsResult(raw string) (EmotionsResult, error) {
	var resp emotionsResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return EmotionsResult{}, fmt.Errorf("%w: decode: %w", ErrEmotionsResponseInvalid, err)
	}

	seen := make(map[models.EmotionValue]struct{}, len(resp.Emotions))
	labels := make([]models.EmotionValue, 0, len(resp.Emotions))

	for _, item := range resp.Emotions {
		label := models.EmotionValue(strings.TrimSpace(item))
		if !label.IsValid() {
			continue
		}

		if _, dup := seen[label]; dup {
			continue
		}

		seen[label] = struct{}{}
		labels = append(labels, label)
	}

	return EmotionsResult{Labels: labels}, nil
}
