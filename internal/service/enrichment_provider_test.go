package service

import (
	"testing"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
)

// TestNewEnrichmentProvider_ValidatesConfig guards the wiring invariants that would otherwise
// nil-panic only on the first eligible event: the required hooks must be set, and a gated
// enrichment must have a resolver. All are enforced fail-fast at construction.
func TestNewEnrichmentProvider_ValidatesConfig(t *testing.T) {
	hasContent := func(*models.FeedbackRecord) bool { return true }
	buildArgs := func(*models.FeedbackRecord, *models.TenantSettings) (river.JobArgs, bool) { return nil, false }

	require.Panics(t, func() {
		newEnrichmentProvider(enrichmentProviderConfig{name: "test", buildArgs: buildArgs})
	}, "a missing required hook (hasContent) must panic at construction")

	require.Panics(t, func() {
		newEnrichmentProvider(enrichmentProviderConfig{name: "test", hasContent: hasContent, buildArgs: buildArgs, gated: true})
	}, "a gated enrichment without a resolver must panic at construction")

	require.NotPanics(t, func() {
		newEnrichmentProvider(enrichmentProviderConfig{name: "test", hasContent: hasContent, buildArgs: buildArgs})
	}, "an ungated enrichment with all required hooks needs no resolver")
}
