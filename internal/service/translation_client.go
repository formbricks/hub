package service

import (
	"context"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// TranslateRequest is the input to a single translation. SourceLang and TargetLang
// are BCP-47 tags: TargetLang comes from the tenant's settings; SourceLang from the
// feedback record's language and may be empty when the source language is unknown.
type TranslateRequest struct {
	Text       string
	SourceLang string
	TargetLang string
}

// TranslationClient translates TranslateRequest.Text from SourceLang into TargetLang
// and returns the translated text. Implementations call an LLM provider (OpenAI or
// Google); the factory selects one from configuration. It mirrors the
// EmbeddingClient seam so the worker depends on the interface, not a provider.
type TranslationClient interface {
	Translate(ctx context.Context, req TranslateRequest) (string, error)
}

// languageDisplayName returns the English display name of a BCP-47 language tag
// (e.g. "de-DE" -> "German") for the translation prompt, mirroring how Formbricks
// passes human-readable language names rather than raw codes to the model. It
// returns "" for an empty tag (an unknown source language) and falls back to the
// trimmed raw tag when it cannot be parsed or named.
func languageDisplayName(tag string) string {
	trimmed := strings.TrimSpace(tag)
	if trimmed == "" {
		return ""
	}

	parsed, err := language.Parse(trimmed)
	if err != nil {
		return trimmed
	}

	if name := display.English.Languages().Name(parsed); name != "" {
		return name
	}

	return trimmed
}
