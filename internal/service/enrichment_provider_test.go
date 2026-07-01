package service

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
)

// TestNewEnrichmentProvider_RequiresResolverWhenGated guards the wiring invariant: a per-tenant
// gate (enabled) needs a resolver to read it, so a config that sets one without the other must
// fail fast at construction rather than nil-panic on the first eligible event.
func TestNewEnrichmentProvider_RequiresResolverWhenGated(t *testing.T) {
	gate := func(models.EnrichmentSettings) bool { return true }

	require.Panics(t, func() {
		NewEnrichmentProvider(enrichmentProviderConfig{name: "test", enabled: gate})
	}, "a gate without a resolver is a wiring bug and must panic at construction")

	require.NotPanics(t, func() {
		NewEnrichmentProvider(enrichmentProviderConfig{name: "test"})
	}, "an enrichment with no per-tenant gate (e.g. embedding) needs no resolver")
}
