package workers

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// FeedbackSentimentWorker classifies a feedback record's value_text into a sentiment label and
// score and stores it — a configured enrichmentWorker. It borrows the shared rate-limit snooze
// (it, too, calls a rate-limited LLM provider) and has no supersession (no per-tenant target).
type FeedbackSentimentWorker = enrichmentWorker[service.FeedbackSentimentArgs, service.SentimentResult]

// sentimentWorkerService is the minimal interface the worker needs.
type sentimentWorkerService interface {
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	SetSentiment(ctx context.Context, feedbackRecordID uuid.UUID, sentiment *models.SentimentValue, score *float64) error
}

const feedbackSentimentTimeout = 30 * time.Second

// NewFeedbackSentimentWorker creates a worker that fetches the record, classifies its value_text,
// and stores the result. metrics may be nil when metrics are disabled.
func NewFeedbackSentimentWorker(
	svc sentimentWorkerService, client service.SentimentClient, metrics observability.SentimentMetrics,
) *FeedbackSentimentWorker {
	return newEnrichmentWorker(enrichmentWorkerConfig[service.FeedbackSentimentArgs, service.SentimentResult]{
		name:       "sentiment",
		timeout:    feedbackSentimentTimeout,
		recordID:   func(args service.FeedbackSentimentArgs) uuid.UUID { return args.FeedbackRecordID },
		getRecord:  svc.GetFeedbackRecord,
		eligible:   (*models.FeedbackRecord).IsTextField,
		hasContent: (*models.FeedbackRecord).HasOpenText,
		classify: func(ctx context.Context, record *models.FeedbackRecord, _ service.FeedbackSentimentArgs) (service.SentimentResult, error) {
			sourceLang := ""
			if record.Language != nil {
				sourceLang = *record.Language
			}

			return client.Classify(ctx, *record.ValueText, sourceLang)
		},
		persist: func(ctx context.Context, id uuid.UUID, _ service.FeedbackSentimentArgs, result *service.SentimentResult) error {
			if result == nil {
				return svc.SetSentiment(ctx, id, nil, nil)
			}

			return svc.SetSentiment(ctx, id, &result.Label, &result.Score)
		},
		rateLimited:     true,
		apiErrorReason:  "sentiment_api_failed",
		classifyErrVerb: "classify",
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
		return enrichmentWorkerMetrics{
			outcome:     func(context.Context, string) {},
			duration:    func(context.Context, time.Duration, string) {},
			workerError: func(context.Context, string) {},
		}
	}

	return enrichmentWorkerMetrics{
		outcome:     m.RecordSentimentOutcome,
		duration:    m.RecordSentimentDuration,
		workerError: m.RecordWorkerError,
	}
}
