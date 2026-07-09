package service

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateTranslationConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     TranslationClientConfig
		wantErr error
	}{
		{name: "openai ok", cfg: TranslationClientConfig{Provider: "openai", ProviderAPIKey: "k", Model: "gpt-x"}},
		{
			name: "openai with base url ok",
			cfg:  TranslationClientConfig{Provider: "openai", ProviderAPIKey: "k", BaseURL: "https://x"},
		},
		{name: "google ok", cfg: TranslationClientConfig{Provider: "google", ProviderAPIKey: "k", Model: "gemini-x"}},
		{
			name: "google-gemini ok",
			cfg:  TranslationClientConfig{Provider: "google-gemini", GoogleCloudProject: "p", GoogleCloudLocation: "l"},
		},
		{
			name: "google-vertex legacy normalizes",
			cfg:  TranslationClientConfig{Provider: "google-vertex", GoogleCloudProject: "p", GoogleCloudLocation: "l"},
		},
		{
			name:    "unsupported provider",
			cfg:     TranslationClientConfig{Provider: "anthropic"},
			wantErr: ErrTranslationConfigInvalid,
		},
		{name: "openai missing key", cfg: TranslationClientConfig{Provider: "openai"}, wantErr: ErrTranslationProviderAPIKey},
		{name: "google missing key", cfg: TranslationClientConfig{Provider: "google"}, wantErr: ErrTranslationProviderAPIKey},
		{
			name:    "base url rejected for google",
			cfg:     TranslationClientConfig{Provider: "google", ProviderAPIKey: "k", BaseURL: "https://x"},
			wantErr: ErrTranslationBaseURLUnsupported,
		},
		{
			name:    "gemini missing project/location",
			cfg:     TranslationClientConfig{Provider: "google-gemini"},
			wantErr: ErrTranslationGoogleGeminiConfig,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := translationClientRegistry.validate(testCase.cfg)

			switch {
			case testCase.wantErr == nil && err != nil:
				t.Fatalf("ValidateTranslationConfig() error = %v, want nil", err)
			case testCase.wantErr != nil && !errors.Is(err, testCase.wantErr):
				t.Fatalf("ValidateTranslationConfig() error = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func TestBuildTranslationPrompt(t *testing.T) {
	t.Run("known source and target render language names", func(t *testing.T) {
		systemPrompt, userText := buildTranslationPrompt(
			TranslateRequest{Text: "Bonjour", SourceLang: "fr", TargetLang: "de-DE"})

		if userText != "Bonjour" {
			t.Fatalf("user text = %q, want Bonjour", userText)
		}

		if !strings.Contains(systemPrompt, "from French into German") {
			t.Fatalf("system prompt missing language names: %q", systemPrompt)
		}

		// The model must not re-flow the text — it was inserting paragraph breaks into
		// run-on source text (see Johannes' German login-issue example).
		if !strings.Contains(systemPrompt, "same line breaks") {
			t.Fatalf("system prompt missing line-break preservation instruction: %q", systemPrompt)
		}
	})

	t.Run("unknown source falls back to original language", func(t *testing.T) {
		systemPrompt, _ := buildTranslationPrompt(
			TranslateRequest{Text: "x", SourceLang: "", TargetLang: "fr"})

		if !strings.Contains(systemPrompt, "from its original language into French") {
			t.Fatalf("system prompt missing original-language fallback: %q", systemPrompt)
		}
	})
}
