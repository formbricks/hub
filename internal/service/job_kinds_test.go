package service

import (
	"testing"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
)

// JobKindSpecs is what the API's queue-depth gauge and the worker-registration parity test both read,
// so the pairing of every kind to its queue is pinned here rather than left implicit.
func TestJobKindSpecs(t *testing.T) {
	want := map[string]string{
		"webhook_dispatch":            river.QueueDefault,
		"feedback_embedding":          EmbeddingsQueueName,
		"feedback_translation":        TranslationsQueueName,
		"tenant_translation_backfill": TranslationBackfillsQueueName,
		"feedback_sentiment":          SentimentsQueueName,
		"feedback_emotions":           EmotionsQueueName,
		"feedback_records_purge":      FeedbackRecordsPurgeQueueName,
		"embedding_reconcile":         EmbeddingReconcileQueueName,
	}

	specs := JobKindSpecs()
	require.Len(t, specs, len(want), "a new job kind needs a worker registered for it in internal/workers")

	for _, spec := range specs {
		wantQueue, ok := want[spec.Kind()]
		require.True(t, ok, "unexpected job kind %q", spec.Kind())
		require.Equal(t, wantQueue, spec.Queue, "kind %q is on the wrong queue", spec.Kind())

		if spec.Kind() == "feedback_embedding" {
			require.Equal(t, EmbeddingsReconcileQueueName, spec.ReconcileQueue)
		} else {
			require.Empty(t, spec.ReconcileQueue)
		}
	}
}

func TestJobQueueNames(t *testing.T) {
	require.Equal(t, []string{
		river.QueueDefault,
		EmbeddingsQueueName,
		EmbeddingsReconcileQueueName,
		TranslationsQueueName,
		TranslationBackfillsQueueName,
		SentimentsQueueName,
		EmotionsQueueName,
		FeedbackRecordsPurgeQueueName,
		EmbeddingReconcileQueueName,
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
