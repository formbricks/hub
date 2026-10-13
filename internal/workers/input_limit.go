package workers

import (
	"context"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// recordTrimmed counts one provider call whose input had to be cut to the configured cap. It is a
// no-op when metrics are disabled, so call sites stay free of nil checks.
func recordTrimmed(ctx context.Context, metrics observability.EnrichmentTrimmedMetrics, enrichment string) {
	if metrics != nil {
		metrics.RecordTrimmed(ctx, enrichment)
	}
}

// trimForProvider cuts text to the deployment's configured cap before a provider call, and counts
// the cut so a silently-shrunk input is visible in metrics rather than only in a config value.
//
// Trimming is the right answer wherever a partial result is still a usable result: a vector or a
// label for the first part of a record beats none at all, and it is what keeps an over-long record
// from becoming a terminal provider failure that leaves it unenriched for good.
//
// Translation deliberately does NOT use this — it refuses over-cap input instead (see translate in
// feedback_translation.go), because a translation of the first N bytes stored as if it were the
// whole text is wrong, where half an embedding is merely less useful.
func trimForProvider(
	ctx context.Context, text string, inputCap service.TextCap,
	metrics observability.EnrichmentTrimmedMetrics, enrichment string,
) string {
	capped, cut := inputCap.Apply(text)
	if cut {
		recordTrimmed(ctx, metrics, enrichment)
	}

	return capped
}

// embeddingEnrichmentName maps an embedding input kind to the label its metrics carry. The raw
// search embedding and the taxonomy's translated-text embedding are separate pipelines that happen
// to share one worker, so a counter that lumped them together would hide which one is trimming.
func embeddingEnrichmentName(kind models.EmbeddingInputKind) string {
	if models.NormalizeEmbeddingInputKind(kind) == models.EmbeddingInputKindTaxonomyTranslated {
		return models.EnrichmentNameTaxonomyEmbedding
	}

	return models.EnrichmentNameEmbedding
}
