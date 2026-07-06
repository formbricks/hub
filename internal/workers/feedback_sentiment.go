package workers

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// FeedbackSentimentWorker classifies a feedback record's value_text into a sentiment label and
// score and stores it — a configured enrichmentWorker. It borrows the shared rate-limit snooze
// (it, too, calls a rate-limited LLM provider); it has no per-tenant target, but its persist is
// guarded against content supersession (a job that read older text skips instead of landing its
// label last — a stale non-NULL label would escape the NULL-rows-only backfill forever).
type FeedbackSentimentWorker = enrichmentWorker[service.FeedbackSentimentArgs, service.SentimentResult]

// sentimentWorkerService is the minimal interface the worker needs.
type sentimentWorkerService interface {
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	SetSentiment(ctx context.Context, feedbackRecordID uuid.UUID, sentiment *models.SentimentValue, score *float64,
		stillCurrent func(valueText *string) bool) error
}

// NewFeedbackSentimentWorker creates a worker that fetches the record, classifies its value_text,
// and stores the result. metrics may be nil when metrics are disabled.
func NewFeedbackSentimentWorker(
	svc sentimentWorkerService, resolver tenantSettingsReader,
	client service.SentimentClient, metrics observability.SentimentMetrics,
) *FeedbackSentimentWorker {
	return newEnrichmentWorker(enrichmentWorkerConfig[service.FeedbackSentimentArgs, service.SentimentResult]{
		name:         "sentiment",
		timeout:      enrichmentJobTimeout,
		recordID:     func(args service.FeedbackSentimentArgs) uuid.UUID { return args.FeedbackRecordID },
		eventID:      func(args service.FeedbackSentimentArgs) uuid.UUID { return args.EventID },
		getRecord:    svc.GetFeedbackRecord,
		eligible:     (*models.FeedbackRecord).IsTextField,
		hasContent:   (*models.FeedbackRecord).HasOpenText,
		checkEnabled: settingsGate(resolver, models.EnrichmentSettings.SentimentEnrichmentEnabled),
		classify: func(ctx context.Context, record *models.FeedbackRecord, _ service.FeedbackSentimentArgs) (service.SentimentResult, error) {
			sourceLang := ""
			if record.Language != nil {
				sourceLang = *record.Language
			}

			return client.Classify(ctx, *record.ValueText, sourceLang)
		},
		persist: func(
			ctx context.Context, record *models.FeedbackRecord, _ service.FeedbackSentimentArgs, result *service.SentimentResult,
		) error {
			// Guard the write against content churn since the Work-time read: a stale job's label
			// (or clear) must not land last over a newer job's write.
			stillCurrent := valueTextStillCurrent(record.ValueText)
			if result == nil {
				return svc.SetSentiment(ctx, record.ID, nil, nil, stillCurrent)
			}

			return svc.SetSentiment(ctx, record.ID, &result.Label, &result.Score, stillCurrent)
		},
		isSuperseded:     func(err error) bool { return errors.Is(err, huberrors.ErrClassificationSuperseded) },
		supersededReason: "superseded",
		rateLimited:      true,
		apiErrorReason:   "sentiment_api_failed",
		classifyErrVerb:  "classify",
		logResult: func(result service.SentimentResult) []any {
			return []any{"sentiment", result.Label, "score", result.Score}
		},
		metrics: sentimentWorkerMetrics(metrics),
	})
}

// sentimentWorkerMetrics adapts SentimentMetrics to the worker's type-agnostic metric hooks,
// installing no-ops when metrics are disabled so the worker never nil-checks.
func sentimentWorkerMetrics(m observability.SentimentMetrics) enrichmentWorkerMetrics {
	if m == nil {
		return noopEnrichmentWorkerMetrics
	}

	return enrichmentWorkerMetrics{
		outcome:     m.RecordSentimentOutcome,
		duration:    m.RecordSentimentDuration,
		workerError: m.RecordWorkerError,
	}
}
