package service

import (
	"context"
	"fmt"

	"github.com/formbricks/hub/internal/llm"
)

// structuredSpec is the per-type contract for a structured (JSON) classification: how to build the
// prompt from the record's text and source language, the output schema, and how to parse and
// validate the JSON into the typed result R. Sentiment provides one today; emotions will provide
// another, both driven by classifyStructured so the provider call, error wrap, and parse flow live
// in one place. (Translation is not structured — it keeps its own plain-text client.)
type structuredSpec[R any] struct {
	// Name labels the enrichment in the classify error wrap (e.g. "sentiment").
	Name        string
	Schema      llm.Schema
	BuildPrompt func(text, sourceLang string) (systemPrompt, userText string)
	Parse       func(raw string) (R, error)
}

// classifyStructured runs one structured classification: build the prompt, call the provider with
// the spec's schema, and parse the JSON result. It is the shared body behind the per-type
// structured clients (sentiment, and later emotions); the raw provider call stays prompt-agnostic.
func classifyStructured[R any](
	ctx context.Context, raw rawStructuredCompleter, spec structuredSpec[R], text, sourceLang string,
) (R, error) {
	var zero R

	systemPrompt, userText := spec.BuildPrompt(text, sourceLang)

	out, err := raw.CompleteJSON(ctx, systemPrompt, userText, spec.Schema)
	if err != nil {
		return zero, fmt.Errorf("classify %s: %w", spec.Name, err)
	}

	return spec.Parse(out)
}
