package workers

import (
	"context"
	"testing"

	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

// The API inserts every kind in service.JobKindSpecs but registers no workers — its River client is
// insert-only (ENG-1916), so River's insert-time unknown-kind check no longer catches a kind that
// nothing registers. These tests are what replaced it: a kind added to JobKindSpecs, or an insert
// path added to the API, without a matching worker here would otherwise strand jobs silently.

// stubTranslationBackfillService satisfies tenantTranslationBackfillService. The tests only register
// workers, never run them, so the method is never called.
type stubTranslationBackfillService struct{}

func (stubTranslationBackfillService) BackfillTranslationsForTenant(
	_ context.Context, _ service.RiverJobInserter, _ string, _ int, _, _ string,
) (int, error) {
	return 0, nil
}

// stubFeedbackRecordsPurgeService satisfies feedbackRecordsPurgeService. The tests only register
// workers, never run them, so the method is never called.
type stubFeedbackRecordsPurgeService struct{}

func (stubFeedbackRecordsPurgeService) Purge(
	_ context.Context, _ string,
) (*models.FeedbackRecordsPurgeCounts, error) {
	return &models.FeedbackRecordsPurgeCounts{}, nil
}

type stubEmbeddingReconcileSweeper struct{}

func (stubEmbeddingReconcileSweeper) Sweep(context.Context) (service.EmbeddingReconcileResult, error) {
	return service.EmbeddingReconcileResult{}, nil
}

// kindProbe re-registers a job kind to observe whether it is already registered.
// river.AddWorkerSafely errors only on a duplicate kind, and *river.Workers exposes no way to
// enumerate its kinds (workersMap is unexported with no accessor), so this is the only way to assert
// registration without a database.
type kindProbe[T river.JobArgs] struct {
	river.WorkerDefaults[T]
}

func (kindProbe[T]) Work(context.Context, *river.Job[T]) error { return nil }

// assertKindRegistered fails unless kind T's registration in workers matches want. It consumes the
// bundle (a probe for an unregistered kind gets added), so pass a bundle you are done asserting on.
func assertKindRegistered[T river.JobArgs](t *testing.T, workers *river.Workers, want bool) {
	t.Helper()

	var args T

	registered := river.AddWorkerSafely(workers, kindProbe[T]{}) != nil
	if registered != want {
		t.Fatalf("kind %q registered = %v, want %v", args.Kind(), registered, want)
	}
}

// fullRiverDeps populates every optional client so NewRiverWorkersAndQueues registers all workers.
// The gate is only that each client is non-nil; the enrichment worker constructors take method
// values off their service dependency, so those must be non-nil too.
func fullRiverDeps() RiverDeps {
	return RiverDeps{
		WebhooksRepo:   &mockDispatchRepo{},
		WebhookSender:  &mockSender{},
		WebhookMetrics: newCountingWebhookMetrics(),

		EmbeddingService:          &mockEmbeddingService{},
		EmbeddingClient:           &mockEmbeddingClient{},
		EmbeddingMetrics:          &countingEmbeddingMetrics{},
		EmbeddingReconcileSweeper: stubEmbeddingReconcileSweeper{},

		TranslationService:         &mockTranslationWorkerService{},
		TranslationClient:          &stubTranslationClient{},
		TranslationMetrics:         newCountingTranslationMetrics(),
		TranslationBackfillService: stubTranslationBackfillService{},

		SentimentService:  &mockSentimentWorkerService{},
		SentimentResolver: stubSentimentSettings{},
		SentimentClient:   &stubSentimentClient{},
		SentimentMetrics:  &countingSentimentMetrics{},

		EmotionsService:  &mockEmotionsWorkerService{},
		EmotionsResolver: stubEmotionsSettings{},
		EmotionsClient:   &stubEmotionsClient{},
		EmotionsMetrics:  &countingEmotionsMetrics{},

		FeedbackRecordsPurgeService: stubFeedbackRecordsPurgeService{},
		ReconcileSweeper:            stubReconcileSweeper{},
	}
}

// stubReconcileSweeper stands in for the reconcile service in wiring tests, which are about
// registration rather than about what a sweep does.
type stubReconcileSweeper struct{}

func (stubReconcileSweeper) Sweep(context.Context) (service.ReconcileResult, error) {
	return service.ReconcileResult{}, nil
}

// TestNewRiverWorkersAndQueuesCoversEveryJobKind locks hub-worker's registration against
// service.JobKindSpecs: every kind the API can insert must have a worker and a declared queue here.
func TestNewRiverWorkersAndQueuesCoversEveryJobKind(t *testing.T) {
	cfg := &config.Config{}

	workerBundle, queues := NewRiverWorkersAndQueues(cfg, fullRiverDeps())

	for _, spec := range service.JobKindSpecs() {
		if _, ok := queues[spec.Queue]; !ok {
			t.Fatalf("queue %q for kind %q missing from queue config, want declared", spec.Queue, spec.Kind())
		}

		// The backfill lane too. A kind whose live queue is declared but whose backfill queue is
		// not would have the reconciler insert onto a queue no worker is assigned to, and those
		// jobs sit there forever looking enqueued.
		if spec.ReconcileQueue == "" {
			continue
		}

		if _, ok := queues[spec.ReconcileQueue]; !ok {
			t.Fatalf("backfill queue %q for kind %q missing from queue config, want declared",
				spec.ReconcileQueue, spec.Kind())
		}
	}

	// One probe per kind: AddWorkerSafely is generic over the concrete args type, so these cannot be
	// looped over the JobKindSpecs slice. The count assertion below is what keeps this list honest.
	assertKindRegistered[service.WebhookDispatchArgs](t, workerBundle, true)
	assertKindRegistered[service.FeedbackEmbeddingArgs](t, workerBundle, true)
	assertKindRegistered[service.FeedbackTranslationArgs](t, workerBundle, true)
	assertKindRegistered[service.TenantTranslationBackfillArgs](t, workerBundle, true)
	assertKindRegistered[service.FeedbackSentimentArgs](t, workerBundle, true)
	assertKindRegistered[service.FeedbackEmotionsArgs](t, workerBundle, true)
	assertKindRegistered[service.FeedbackRecordsPurgeArgs](t, workerBundle, true)
	assertKindRegistered[service.EmbeddingReconcileArgs](t, workerBundle, true)
	assertKindRegistered[service.EnrichmentReconcileArgs](t, workerBundle, true)

	const probedKinds = 9
	if got := len(service.JobKindSpecs()); got != probedKinds {
		t.Fatalf("JobKindSpecs has %d kinds but %d are probed above — add a probe for the new kind "+
			"and register a worker for it in NewRiverWorkersAndQueues", got, probedKinds)
	}
}

// TestNewRiverWorkersAndQueuesWithoutOptionalClients pins the disabled-enrichment shape: the
// always-on workers (webhook dispatch and the feedback-records purge) still run, and a nil client
// must leave both its worker and its queue out rather than registering a worker that would fail on
// every job.
func TestNewRiverWorkersAndQueuesWithoutOptionalClients(t *testing.T) {
	cfg := &config.Config{}
	deps := RiverDeps{
		WebhooksRepo:   &mockDispatchRepo{},
		WebhookSender:  &mockSender{},
		WebhookMetrics: newCountingWebhookMetrics(),

		FeedbackRecordsPurgeService: stubFeedbackRecordsPurgeService{},
	}

	workerBundle, queues := NewRiverWorkersAndQueues(cfg, deps)

	alwaysOnQueues := []string{river.QueueDefault, service.FeedbackRecordsPurgeQueueName}
	if len(queues) != len(alwaysOnQueues) {
		t.Fatalf("queues = %v, want only %v", queues, alwaysOnQueues)
	}

	for _, name := range alwaysOnQueues {
		if _, ok := queues[name]; !ok {
			t.Fatalf("queues = %v, want %q declared", queues, name)
		}
	}

	assertKindRegistered[service.WebhookDispatchArgs](t, workerBundle, true)
	assertKindRegistered[service.FeedbackRecordsPurgeArgs](t, workerBundle, true)
	assertKindRegistered[service.FeedbackEmbeddingArgs](t, workerBundle, false)
	assertKindRegistered[service.FeedbackTranslationArgs](t, workerBundle, false)
	assertKindRegistered[service.TenantTranslationBackfillArgs](t, workerBundle, false)
	assertKindRegistered[service.FeedbackSentimentArgs](t, workerBundle, false)
	assertKindRegistered[service.FeedbackEmotionsArgs](t, workerBundle, false)
}

// TestNewRiverWorkersAndQueuesDrainsExistingRepairsWhenSweeperDisabled ensures disabling future
// sweeps cannot strand repair jobs that were queued by an earlier worker configuration.
func TestNewRiverWorkersAndQueuesDrainsExistingRepairsWhenSweeperDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Embedding.MaxConcurrent = 3
	cfg.Embedding.ReconcileMaxConcurrent = 1
	deps := fullRiverDeps()
	deps.EmbeddingReconcileSweeper = nil

	workerBundle, queues := NewRiverWorkersAndQueues(cfg, deps)

	if got := queues[service.EmbeddingsReconcileQueueName].MaxWorkers; got != 1 {
		t.Fatalf("queue %q MaxWorkers = %d, want 1", service.EmbeddingsReconcileQueueName, got)
	}

	_, sweepQueueRegistered := queues[service.EmbeddingReconcileQueueName]
	if sweepQueueRegistered {
		t.Fatalf("queue %q registered with sweeper disabled, want absent", service.EmbeddingReconcileQueueName)
	}

	assertKindRegistered[service.FeedbackEmbeddingArgs](t, workerBundle, true)
	assertKindRegistered[service.EmbeddingReconcileArgs](t, workerBundle, false)
}

// TestNewRiverWorkersAndQueuesUsesConfiguredConcurrency pins each queue to its own configured
// concurrency. hub-worker is the only caller, so a queue silently picking up another enrichment's
// limit — or a zero, which would stall it entirely — would otherwise go unnoticed.
func TestNewRiverWorkersAndQueuesUsesConfiguredConcurrency(t *testing.T) {
	cfg := &config.Config{}
	cfg.Webhook.DeliveryMaxConcurrent = 2
	cfg.Embedding.MaxConcurrent = 3
	cfg.Translation.MaxConcurrent = 4
	cfg.Sentiment.MaxConcurrent = 5
	cfg.Emotions.MaxConcurrent = 6

	_, queues := NewRiverWorkersAndQueues(cfg, fullRiverDeps())

	want := map[string]int{
		river.QueueDefault:                    2,
		service.EmbeddingsQueueName:           3,
		service.TranslationsQueueName:         4,
		service.TranslationBackfillsQueueName: 4,
		service.SentimentsQueueName:           5,
		service.EmotionsQueueName:             6,
		// Purges are deliberately serialized rather than configurable — see wiring.go.
		service.FeedbackRecordsPurgeQueueName: feedbackRecordsPurgeMaxWorkers,
	}

	for name, wantMax := range want {
		if got := queues[name].MaxWorkers; got != wantMax {
			t.Fatalf("queue %q MaxWorkers = %d, want %d", name, got, wantMax)
		}
	}
}
