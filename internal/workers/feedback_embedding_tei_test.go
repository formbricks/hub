package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/openai"
	"github.com/formbricks/hub/internal/service"
)

// TestFeedbackEmbeddingWorkerTEILengthErrorIsolatesValidPeers exercises TEI's real 422
// envelope through the SDK, batcher, and workers: valid peers store vectors on their first
// attempt, while only the over-length record receives a terminal marker for reconciliation.
func TestFeedbackEmbeddingWorkerTEILengthErrorIsolatesValidPeers(t *testing.T) {
	const (
		count        = 8
		invalidInput = "over-length"
	)

	var batches, singles atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode embedding request: %v", err)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		var inputs []string

		if strings.HasPrefix(string(request.Input), "[") {
			batches.Add(1)

			if err := json.Unmarshal(request.Input, &inputs); err != nil {
				t.Errorf("decode batch inputs: %v", err)
				w.WriteHeader(http.StatusBadRequest)

				return
			}
		} else {
			singles.Add(1)

			var input string
			if err := json.Unmarshal(request.Input, &input); err != nil {
				t.Errorf("decode single input: %v", err)
				w.WriteHeader(http.StatusBadRequest)

				return
			}

			inputs = []string{input}
		}

		w.Header().Set("Content-Type", "application/json")

		for _, input := range inputs {
			if strings.Contains(input, invalidInput) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message":"` + "`inputs`" + ` must have less than 8192 tokens. Given: 8193","code":422,"type":"validation"}`))

				return
			}
		}

		data := make([]map[string]any, len(inputs))
		for i := range inputs {
			data[i] = map[string]any{"index": i, "embedding": []float64{1, 2}, "object": "embedding"}
		}

		if err := json.NewEncoder(w).Encode(map[string]any{"data": data, "model": "test-model", "object": "list"}); err != nil {
			t.Errorf("encode vectors: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	client := openai.NewClient("test-key", openai.WithBaseURL(server.URL+"/v1"),
		openai.WithModel("test-model"), openai.WithDimensions(2))
	batcher, enabled := service.NewBatchingEmbeddingClient(client, service.EmbeddingBatchConfig{
		BatchSize: count, MaxWait: time.Minute, MaxInFlight: 1,
	}, nil)
	require.True(t, enabled)

	errs := make([]error, count)
	services := make([]*mockEmbeddingService, count)

	failures := make([]*recordingFailureRecorder, count)
	for i := range count {
		input := fmt.Sprintf("valid-%d", i)
		if i == count-1 {
			input = invalidInput
		}

		record := translatedTextRecord(input, input)
		record.ID = uuid.Must(uuid.NewV7())
		record.TenantID = "tenant-tei-isolation"
		services[i] = &mockEmbeddingService{record: record}
		failures[i] = &recordingFailureRecorder{}
	}

	var group sync.WaitGroup
	for i := range count {
		group.Go(func() {
			worker := NewFeedbackEmbeddingWorkerWithOptions(services[i], batcher, "", nil,
				time.Minute, failures[i], nil)
			job := taxonomyEmbeddingJob(1)
			job.Args.FeedbackRecordID = services[i].record.ID
			errs[i] = worker.Work(context.Background(), job)
		})
	}

	group.Wait()

	for i := range count - 1 {
		require.NoError(t, errs[i], "valid peer %d", i)
		assert.Equal(t, 1, services[i].setCalls, "valid peer %d stores its vector", i)
		assert.False(t, services[i].setEmbeddingNil)
		assert.Empty(t, failures[i].calls)
	}

	var canceled *river.JobCancelError
	require.ErrorAs(t, errs[count-1], &canceled)
	assert.Zero(t, services[count-1].setCalls)
	require.Len(t, failures[count-1].calls, 1)
	failure := failures[count-1].calls[0]
	assert.True(t, failure.Terminal)
	assert.Equal(t, string(huberrors.TerminalReasonLength), failure.Reason)
	assert.Equal(t, "tenant-tei-isolation", failure.TenantID)
	assert.Equal(t, 1, failure.Attempts)
	assert.EqualValues(t, 1, batches.Load())
	assert.EqualValues(t, count, singles.Load())
}
