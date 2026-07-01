package service

import (
	"context"
	"fmt"
	"strings"
)

// Provider identifiers shared by every enrichment client — sentiment, translation, and
// embedding all reuse the same OpenAI and Google SDK wrappers, so the provider names are
// common. Per-type factories alias these (e.g. SentimentProviderOpenAI) for readability.
const (
	ProviderOpenAI       = "openai"
	ProviderGoogle       = "google"
	ProviderGoogleGemini = "google-gemini"

	// providerGoogleVertexLegacy is the pre-rename name for google-gemini; registries that opt
	// in (allowVertexAlias) normalize it to ProviderGoogleGemini for backward compatibility.
	providerGoogleVertexLegacy = "google-vertex"
)

// enrichmentClientConfig is the common shape the generic registry reads to validate and
// dispatch. Every per-type client config satisfies it via small accessors; per-type extras
// (e.g. embedding's Normalize) are read by that type's build funcs, which receive the concrete
// config type, so they never need to appear here.
type enrichmentClientConfig interface {
	clientProvider() string
	clientAPIKey() string
	clientBaseURL() string
	clientGoogleCloudProject() string
	clientGoogleCloudLocation() string
}

// providerFactory describes one provider's capabilities and how to build a client of type T
// from config C.
type providerFactory[C enrichmentClientConfig, T any] struct {
	requiresAPIKey             bool
	requiresGoogleGeminiConfig bool
	build                      func(context.Context, C) (T, error)
}

// clientRegistry is the shared provider registry + validate/dispatch for one enrichment type.
// It removes the copy-pasted registry/validate/dispatch scaffold that each enrichment used to
// carry. Per-type sentinel errors (which name the type's own env vars) are injected so callers'
// errors.Is checks keep matching the same values, and allowVertexAlias opts a type into the
// legacy google-vertex → google-gemini normalization.
type clientRegistry[C enrichmentClientConfig, T any] struct {
	entries          map[string]providerFactory[C, T]
	allowVertexAlias bool

	errConfigInvalid error // unsupported provider
	errAPIKey        error // API-key provider configured without a key
	errBaseURL       error // custom base URL on a non-openai provider
	errGoogleGemini  error // google-gemini without project/location
}

// normalize returns the canonical provider name (lowercased, trimmed), applying the legacy
// google-vertex alias when the registry opts in.
func (r clientRegistry[C, T]) normalize(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if r.allowVertexAlias && provider == providerGoogleVertexLegacy {
		return ProviderGoogleGemini
	}

	return provider
}

// lookup normalizes the configured provider and returns it alongside its registry entry, or the
// type's config-invalid error when the provider is unsupported. It is the single resolution point
// shared by validate and newClient.
func (r clientRegistry[C, T]) lookup(cfg C) (string, providerFactory[C, T], error) {
	provider := r.normalize(cfg.clientProvider())

	entry, ok := r.entries[provider]
	if !ok {
		return provider, providerFactory[C, T]{}, fmt.Errorf("%w: unsupported provider %q", r.errConfigInvalid, provider)
	}

	return provider, entry, nil
}

// checkRequirements verifies the provider-specific requirements (API key, base URL, gemini
// project/location) for an already-resolved provider entry, returning the type's own sentinels.
func (r clientRegistry[C, T]) checkRequirements(provider string, entry providerFactory[C, T], cfg C) error {
	if entry.requiresAPIKey && cfg.clientAPIKey() == "" {
		return fmt.Errorf("%w: %s", r.errAPIKey, provider)
	}

	if cfg.clientBaseURL() != "" && provider != ProviderOpenAI {
		return fmt.Errorf("%w: %s", r.errBaseURL, provider)
	}

	if entry.requiresGoogleGeminiConfig &&
		(cfg.clientGoogleCloudProject() == "" || cfg.clientGoogleCloudLocation() == "") {
		return r.errGoogleGemini
	}

	return nil
}

// validate checks provider support and provider-specific requirements, returning the type's own
// sentinel errors. Used before creating a client or at startup to fail fast.
func (r clientRegistry[C, T]) validate(cfg C) error {
	provider, entry, err := r.lookup(cfg)
	if err != nil {
		return err
	}

	return r.checkRequirements(provider, entry, cfg)
}

// newClient validates the config, then builds the client via the registry factory, resolving the
// provider entry a single time.
func (r clientRegistry[C, T]) newClient(ctx context.Context, cfg C) (T, error) {
	var zero T

	provider, entry, err := r.lookup(cfg)
	if err != nil {
		return zero, err
	}

	if err := r.checkRequirements(provider, entry, cfg); err != nil {
		return zero, err
	}

	return entry.build(ctx, cfg)
}

// requiresAPIKey reports whether the (normalized) provider needs an API key.
func (r clientRegistry[C, T]) requiresAPIKey(provider string) bool {
	entry, ok := r.entries[r.normalize(provider)]

	return ok && entry.requiresAPIKey
}

// requiresGoogleGeminiConfig reports whether the (normalized) provider needs Google Cloud project/location.
func (r clientRegistry[C, T]) requiresGoogleGeminiConfig(provider string) bool {
	entry, ok := r.entries[r.normalize(provider)]

	return ok && entry.requiresGoogleGeminiConfig
}

// supportedProviders returns the set of supported provider names.
func (r clientRegistry[C, T]) supportedProviders() map[string]struct{} {
	out := make(map[string]struct{}, len(r.entries))
	for name := range r.entries {
		out[name] = struct{}{}
	}

	return out
}
