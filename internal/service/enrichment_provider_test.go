package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewEnrichmentProvider_RequiresResolverWhenGated guards the wiring invariant: a gated
// enrichment needs a resolver to read its settings, so a config that is gated without a resolver
// must fail fast at construction rather than nil-panic on the first eligible event.
func TestNewEnrichmentProvider_RequiresResolverWhenGated(t *testing.T) {
	require.Panics(t, func() {
		NewEnrichmentProvider(enrichmentProviderConfig{name: "test", gated: true})
	}, "a gated enrichment without a resolver is a wiring bug and must panic at construction")

	require.NotPanics(t, func() {
		NewEnrichmentProvider(enrichmentProviderConfig{name: "test"})
	}, "an ungated enrichment (e.g. embedding) needs no resolver")
}
