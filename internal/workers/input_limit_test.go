package workers

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// countingTrimmedMetrics records trimmed-input counts by enrichment label.
type countingTrimmedMetrics struct {
	counts map[string]int
}

func newCountingTrimmedMetrics() *countingTrimmedMetrics {
	return &countingTrimmedMetrics{counts: map[string]int{}}
}

func (m *countingTrimmedMetrics) RecordTrimmed(_ context.Context, enrichment string) {
	m.counts[enrichment]++
}

var _ observability.EnrichmentTrimmedMetrics = (*countingTrimmedMetrics)(nil)

func TestTrimForProvider(t *testing.T) {
	t.Run("under the cap is passed through uncounted", func(t *testing.T) {
		metrics := newCountingTrimmedMetrics()

		got := trimForProvider(context.Background(), "short", service.TextCap{MaxBytes: 100},
			metrics, models.EnrichmentNameSentiment)

		assert.Equal(t, "short", got)
		assert.Empty(t, metrics.counts)
	})
	t.Run("over the cap is cut and counted", func(t *testing.T) {
		metrics := newCountingTrimmedMetrics()
		text := strings.Repeat("a", 50)

		got := trimForProvider(context.Background(), text, service.TextCap{MaxBytes: 10},
			metrics, models.EnrichmentNameSentiment)

		assert.Equal(t, strings.Repeat("a", 10), got)
		assert.Equal(t, 1, metrics.counts[models.EnrichmentNameSentiment])
	})
	t.Run("a nil metrics value still trims", func(t *testing.T) {
		// The cap is a cost and reliability bound, not an observability feature: a deployment with
		// metrics switched off must still not send a 30k answer to a provider.
		text := strings.Repeat("a", 50)

		got := trimForProvider(context.Background(), text, service.TextCap{MaxBytes: 10},
			nil, models.EnrichmentNameSentiment)

		assert.Equal(t, strings.Repeat("a", 10), got)
	})
	t.Run("an unlimited cap passes anything through", func(t *testing.T) {
		text := strings.Repeat("a", 50)

		got := trimForProvider(context.Background(), text, service.TextCap{},
			newCountingTrimmedMetrics(), models.EnrichmentNameSentiment)

		assert.Equal(t, text, got)
	})
}

// TestEmbeddingEnrichmentName pins the label split: the raw search embedding and the taxonomy
// embedding share one worker but are different pipelines, and a counter that conflated them would
// not say which one is cutting text.
func TestEmbeddingEnrichmentName(t *testing.T) {
	assert.Equal(t, models.EnrichmentNameEmbedding, models.EmbeddingInputKindRaw.EnrichmentName())
	assert.Equal(t, models.EnrichmentNameTaxonomyEmbedding,
		models.EmbeddingInputKindTaxonomyTranslated.EnrichmentName())
}
