package workers

import (
	"context"
	"errors"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// errInputOverCap marks a refusal Hub made itself, before any provider call, because the record's
// text is over the configured cap (ENG-3908; today only translation refuses, the rest trim). It
// travels inside the TerminalProviderError so the shared worker gives up the same way it does for a
// provider's own length rejection, but the worker checks for it to keep the two apart in metrics
// and logs: no provider was called, so nothing may count as a provider failure.
var errInputOverCap = errors.New("input is over the configured cap")

// inputOverCapReason is the worker-error reason a pre-call refusal is counted under, in place of
// the type's {type}_api_failed. It is the skip counter the ticket asks for, per enrichment by
// virtue of living in each type's own worker-errors family.
const inputOverCapReason = "input_over_cap"

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
