package service

import (
	"testing"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
)

// JobKindSpecs is what the API's queue-depth gauge and the worker-registration parity test both read,
// so the pairing of every kind to its queue is pinned here rather than left implicit.
func TestJobKindSpecs(t *testing.T) {
	type lanes struct {
		live     string
		backfill string
	}

	// The three record-level enrichments carry a backfill lane; nothing else does. That asymmetry
	// is the design, not an omission: only work the reconciler can re-derive from the records
	// themselves has a second lane to put it on.
	want := map[string]lanes{
		"webhook_dispatch":            {live: river.QueueDefault},
		"feedback_embedding":          {live: EmbeddingsQueueName},
		"feedback_translation":        {live: TranslationsQueueName, backfill: TranslationsBackfillQueueName},
		"tenant_translation_backfill": {live: TranslationBackfillsQueueName},
		"feedback_sentiment":          {live: SentimentsQueueName, backfill: SentimentsBackfillQueueName},
		"feedback_emotions":           {live: EmotionsQueueName, backfill: EmotionsBackfillQueueName},
		"feedback_records_purge":      {live: FeedbackRecordsPurgeQueueName},
		"enrichment_reconcile":        {live: EnrichmentReconcileQueueName},
	}

	specs := JobKindSpecs()
	require.Len(t, specs, len(want), "a new job kind needs a worker registered for it in internal/workers")

	for _, spec := range specs {
		wantLanes, ok := want[spec.Kind()]
		require.True(t, ok, "unexpected job kind %q", spec.Kind())
		require.Equal(t, wantLanes.live, spec.Queue, "kind %q is on the wrong queue", spec.Kind())
		require.Equal(t, wantLanes.backfill, spec.BackfillQueue,
			"kind %q has the wrong backfill lane", spec.Kind())
	}
}

func TestJobQueueNames(t *testing.T) {
	// Both lanes, in declaration order. The backfill queues belong on the depth gauge for the same
	// reason the live ones do: a sweep that is enqueueing but not draining is exactly what an
	// operator needs to see, and it is invisible if the queue has no series.
	require.Equal(t, []string{
		river.QueueDefault,
		EmbeddingsQueueName,
		TranslationsQueueName,
		TranslationsBackfillQueueName,
		TranslationBackfillsQueueName,
		SentimentsQueueName,
		SentimentsBackfillQueueName,
		EmotionsQueueName,
		EmotionsBackfillQueueName,
		FeedbackRecordsPurgeQueueName,
		EnrichmentReconcileQueueName,
	}, JobQueueNames())
}

// The dedup only matters for a future kind that shares an existing queue, which no declared kind
// does — so it is exercised directly rather than through JobKindSpecs.
func TestDistinctQueues(t *testing.T) {
	tests := map[string]struct {
		specs []JobKindSpec
		want  []string
	}{
		"collapses repeats and keeps first-seen order": {
			specs: []JobKindSpec{
				{Args: FeedbackSentimentArgs{}, Queue: "enrichments"},
				{Args: FeedbackEmotionsArgs{}, Queue: "enrichments"},
				{Args: WebhookDispatchArgs{}, Queue: river.QueueDefault},
				{Args: FeedbackTranslationArgs{}, Queue: "enrichments"},
			},
			want: []string{"enrichments", river.QueueDefault},
		},
		"passes distinct queues through unchanged": {
			specs: []JobKindSpec{
				{Args: WebhookDispatchArgs{}, Queue: river.QueueDefault},
				{Args: FeedbackSentimentArgs{}, Queue: SentimentsQueueName},
			},
			want: []string{river.QueueDefault, SentimentsQueueName},
		},
		"handles no specs": {
			specs: nil,
			want:  []string{},
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, testCase.want, distinctQueues(testCase.specs))
		})
	}
}
