package workers

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// FeedbackEmotionsWorker classifies a feedback record's value_text into a set of emotion labels and
// stores it — a configured enrichmentWorker. It borrows the shared rate-limit snooze (it, too,
// calls a rate-limited LLM provider) and has no supersession (no per-tenant target).
type FeedbackEmotionsWorker = enrichmentWorker[service.FeedbackEmotionsArgs, service.EmotionsResult]

// emotionsWorkerService is the minimal interface the worker needs.
type emotionsWorkerService interface {
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	SetEmotions(ctx context.Context, feedbackRecordID uuid.UUID, emotions []models.EmotionValue) error
}

const feedbackEmotionsTimeout = 30 * time.Second

// NewFeedbackEmotionsWorker creates a worker that fetches the record, classifies its value_text,
// and stores the emotion labels. metrics may be nil when metrics are disabled.
func NewFeedbackEmotionsWorker(
	svc emotionsWorkerService, client service.EmotionsClient, metrics observability.EmotionsMetrics,
) *FeedbackEmotionsWorker {
	return newEnrichmentWorker(enrichmentWorkerConfig[service.FeedbackEmotionsArgs, service.EmotionsResult]{
		name:       "emotions",
		timeout:    feedbackEmotionsTimeout,
		recordID:   func(args service.FeedbackEmotionsArgs) uuid.UUID { return args.FeedbackRecordID },
		getRecord:  svc.GetFeedbackRecord,
		eligible:   (*models.FeedbackRecord).IsTextField,
		hasContent: (*models.FeedbackRecord).HasOpenText,
		classify: func(ctx context.Context, record *models.FeedbackRecord, _ service.FeedbackEmotionsArgs) (service.EmotionsResult, error) {
			sourceLang := ""
			if record.Language != nil {
				sourceLang = *record.Language
			}

			return client.Classify(ctx, *record.ValueText, sourceLang)
		},
		persist: func(ctx context.Context, id uuid.UUID, _ service.FeedbackEmotionsArgs, result *service.EmotionsResult) error {
			// A nil result (empty content) or an empty label set both clear the column: absence is
			// NULL, never an empty array.
			if result == nil {
				return svc.SetEmotions(ctx, id, nil)
			}

			return svc.SetEmotions(ctx, id, result.Labels)
		},
		rateLimited:     true,
		apiErrorReason:  "emotions_api_failed",
		classifyErrVerb: "classify",
		logResult: func(result service.EmotionsResult) []any {
			return []any{"emotions", result.Labels}
		},
		metrics: emotionsWorkerMetrics(metrics),
	})
}

// emotionsWorkerMetrics adapts EmotionsMetrics to the worker's type-agnostic metric hooks,
// installing no-ops when metrics are disabled so the worker never nil-checks.
func emotionsWorkerMetrics(m observability.EmotionsMetrics) enrichmentWorkerMetrics {
	if m == nil {
		return enrichmentWorkerMetrics{
			outcome:     func(context.Context, string) {},
			duration:    func(context.Context, time.Duration, string) {},
			workerError: func(context.Context, string) {},
		}
	}

	return enrichmentWorkerMetrics{
		outcome:     m.RecordEmotionsOutcome,
		duration:    m.RecordEmotionsDuration,
		workerError: m.RecordWorkerError,
	}
}
