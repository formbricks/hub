package service

import "github.com/riverqueue/river"

// JobKindSpec pairs a River job kind with the queue its inserts land on.
type JobKindSpec struct {
	// Args is a zero value of the job's argument type; Args.Kind() is the River job kind.
	Args river.JobArgs
	// Queue is the River queue the kind is inserted on.
	Queue string
}

// Kind returns the River job kind this spec describes.
func (s JobKindSpec) Kind() string { return s.Args.Kind() }

// JobKindSpecs enumerates the Hub's River job kinds and the queues they are inserted on. It is the
// declaration the API's queue-depth poller derives from, and the reference the parity test in
// internal/workers checks hub-worker's registration against.
//
// hub-worker's registration (workers.NewRiverWorkersAndQueues) deliberately still names its queues
// itself, because it attaches a per-enrichment MaxWorkers that this list has no notion of. So the
// two are kept consistent by assertion, not by derivation — the parity test is what fails when they
// drift, and it is the reason adding a kind here is safe.
//
// The API inserts every kind below but works none of them: its River client is insert-only, so
// hub-worker owns registration. Adding a kind here without registering a worker for it strands its
// jobs, which is exactly what that test catches.
func JobKindSpecs() []JobKindSpec {
	return []JobKindSpec{
		{Args: WebhookDispatchArgs{}, Queue: river.QueueDefault},
		{Args: FeedbackEmbeddingArgs{}, Queue: EmbeddingsQueueName},
		{Args: FeedbackTranslationArgs{}, Queue: TranslationsQueueName},
		{Args: TenantTranslationBackfillArgs{}, Queue: TranslationBackfillsQueueName},
		{Args: FeedbackSentimentArgs{}, Queue: SentimentsQueueName},
		{Args: FeedbackEmotionsArgs{}, Queue: EmotionsQueueName},
	}
}

// JobQueueNames returns the distinct River queue names from JobKindSpecs, in declaration order.
// Several kinds may share a queue, so callers that need queues rather than kinds use this.
func JobQueueNames() []string {
	return distinctQueues(JobKindSpecs())
}

// distinctQueues collapses specs to their queue names, preserving declaration order and dropping
// repeats. Taking the specs as a parameter keeps the dedup reachable from a test: every kind
// declared today owns its own queue, so the duplicate path would otherwise never run.
func distinctQueues(specs []JobKindSpec) []string {
	seen := make(map[string]struct{}, len(specs))
	queues := make([]string, 0, len(specs))

	for _, spec := range specs {
		if _, ok := seen[spec.Queue]; ok {
			continue
		}

		seen[spec.Queue] = struct{}{}
		queues = append(queues, spec.Queue)
	}

	return queues
}
