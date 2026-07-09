package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/formbricks/hub/internal/googleai"
	"github.com/formbricks/hub/internal/openai"
)

var (
	// ErrTranslationConfigInvalid is returned when the translation provider is unsupported.
	ErrTranslationConfigInvalid = errors.New("translation config invalid")
	// ErrTranslationProviderAPIKey is returned when an API-key-based provider is configured without a key.
	ErrTranslationProviderAPIKey = errors.New("TRANSLATION_PROVIDER_API_KEY is required for this provider")
	// ErrTranslationBaseURLUnsupported is returned when a custom base URL is configured for a non-openai provider.
	ErrTranslationBaseURLUnsupported = errors.New("TRANSLATION_BASE_URL is only supported for openai")
	// ErrTranslationGoogleGeminiConfig is returned when google-gemini is configured without project or location.
	ErrTranslationGoogleGeminiConfig = errors.New(
		"google-gemini requires TRANSLATION_GOOGLE_CLOUD_PROJECT and TRANSLATION_GOOGLE_CLOUD_LOCATION")
)

// rawTranslator is the low-level provider call (system prompt + user text -> text),
// satisfied by *openai.Client and *googleai.Client.
type rawTranslator interface {
	Translate(ctx context.Context, systemPrompt, userText string) (string, error)
}

// promptTranslationClient adapts a rawTranslator to TranslationClient by building
// the translation prompt from the request (the provider call stays prompt-agnostic).
type promptTranslationClient struct {
	raw rawTranslator
}

// Translate builds the prompt and delegates to the provider.
func (c promptTranslationClient) Translate(ctx context.Context, req TranslateRequest) (string, error) {
	systemPrompt, userText := buildTranslationPrompt(req)

	translated, err := c.raw.Translate(ctx, systemPrompt, userText)
	if err != nil {
		return "", fmt.Errorf("translate: %w", err)
	}

	return translated, nil
}

// buildTranslationPrompt renders the system prompt and user text for a request. It
// mirrors Formbricks' "professional translator" instruction, using human-readable
// language names and preserving placeholders/markup. When the source language is
// unknown it asks the model to translate from the original language.
func buildTranslationPrompt(req TranslateRequest) (systemPrompt, userText string) {
	target := languageDisplayName(req.TargetLang)
	if target == "" {
		target = strings.TrimSpace(req.TargetLang)
	}

	from := "its original language"
	if source := languageDisplayName(req.SourceLang); source != "" {
		from = source
	}

	systemPrompt = fmt.Sprintf(
		"You are a professional translator. Translate the user's message from %s into %s. "+
			"Preserve the original meaning and tone. Reproduce the original formatting exactly: keep the "+
			"same line breaks, blank lines, and leading/trailing whitespace, and do not add, remove, or "+
			"merge line breaks or paragraphs. Keep any {{variable}} placeholders and HTML tags unchanged. "+
			"Respond with only the translated text — no preamble, notes, or quotation marks.",
		from, target,
	)

	return systemPrompt, req.Text
}

// TranslationClientConfig aliases the shared classify client config (see EnrichmentClientConfig).
type TranslationClientConfig = EnrichmentClientConfig

// translationClientRegistry is the single source of truth for translation provider capabilities
// and client creation, backed by the shared generic registry. Translation accepts the legacy
// google-vertex alias.
var translationClientRegistry = clientRegistry[TranslationClientConfig, TranslationClient]{
	allowVertexAlias: true,
	errConfigInvalid: ErrTranslationConfigInvalid,
	errAPIKey:        ErrTranslationProviderAPIKey,
	errBaseURL:       ErrTranslationBaseURLUnsupported,
	errGoogleGemini:  ErrTranslationGoogleGeminiConfig,
	entries: map[string]providerFactory[TranslationClientConfig, TranslationClient]{
		ProviderOpenAI:       {requiresAPIKey: true, build: openAITranslationFactory},
		ProviderGoogle:       {requiresAPIKey: true, build: googleTranslationFactory},
		ProviderGoogleGemini: {requiresGoogleGeminiConfig: true, build: googleGeminiTranslationFactory},
	},
}

func openAITranslationFactory(_ context.Context, cfg TranslationClientConfig) (TranslationClient, error) {
	raw := openai.NewClient(cfg.ProviderAPIKey,
		openai.WithModel(cfg.Model),
		openai.WithBaseURL(cfg.BaseURL),
	)

	return promptTranslationClient{raw: raw}, nil
}

func googleTranslationFactory(ctx context.Context, cfg TranslationClientConfig) (TranslationClient, error) {
	raw, err := googleai.NewClient(ctx, cfg.ProviderAPIKey, googleai.WithModel(cfg.Model))
	if err != nil {
		return nil, fmt.Errorf("create google translation client: %w", err)
	}

	return promptTranslationClient{raw: raw}, nil
}

func googleGeminiTranslationFactory(ctx context.Context, cfg TranslationClientConfig) (TranslationClient, error) {
	raw, err := googleai.NewGoogleGeminiClient(ctx, cfg.GoogleCloudProject, cfg.GoogleCloudLocation,
		googleai.WithModel(cfg.Model))
	if err != nil {
		return nil, fmt.Errorf("create google-gemini translation client: %w", err)
	}

	return promptTranslationClient{raw: raw}, nil
}

// NewTranslationClient creates a TranslationClient for the given config. It validates
// provider-specific requirements via the registry, then calls the registry factory.
func NewTranslationClient(ctx context.Context, cfg TranslationClientConfig) (TranslationClient, error) {
	return translationClientRegistry.newClient(ctx, cfg)
}
