package observability

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingHandler keeps every record it is given.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler            { return h }

func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, record)

	return nil
}

func (h *recordingHandler) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	messages := make([]string, 0, len(h.records))
	for _, record := range h.records {
		messages = append(messages, record.Message)
	}

	return messages
}

func (h *recordingHandler) attr(index int, key string) slog.Value {
	h.mu.Lock()
	defer h.mu.Unlock()

	var found slog.Value

	h.records[index].Attrs(func(attr slog.Attr) bool {
		if attr.Key == key {
			found = attr.Value

			return false
		}

		return true
	})

	return found
}

// fakeClock is a settable time source for the sampler.
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

var testSampling = LogSamplingConfig{
	Window:     time.Second,
	First:      3,
	Thereafter: 5,
	KeyAttrs:   []string{"job_kind"},
}

func newTestSampler(t *testing.T, cfg LogSamplingConfig) (*slog.Logger, *recordingHandler, *fakeClock) {
	t.Helper()

	inner := &recordingHandler{}
	clock := &fakeClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}

	handler, ok := NewSamplingHandler(inner, cfg).(*SamplingHandler)
	require.True(t, ok, "a config with a window must wrap the inner handler")

	handler.sampler.now = clock.Now

	return slog.New(handler), inner, clock
}

func logN(logger *slog.Logger, n int, msg string, args ...any) {
	for range n {
		logger.Info(msg, args...)
	}
}

func TestSamplingHandlerPassesTrafficBelowFirst(t *testing.T) {
	logger, inner, _ := newTestSampler(t, testSampling)

	logN(logger, testSampling.First, "Job errored; retrying", "job_kind", "feedback_embedding")

	assert.Len(t, inner.records, testSampling.First, "ordinary traffic must never be sampled")
}

func TestSamplingHandlerSamplesAStorm(t *testing.T) {
	logger, inner, _ := newTestSampler(t, testSampling)

	// First 3 pass, then the 5th, 10th and 15th after them: lines 8, 13 and 18 of 20.
	logN(logger, 20, "Job errored; retrying", "job_kind", "feedback_embedding")

	assert.Len(t, inner.records, 6)
}

func TestSamplingHandlerZeroThereafterDropsEverythingAfterFirst(t *testing.T) {
	cfg := testSampling
	cfg.Thereafter = 0

	logger, inner, _ := newTestSampler(t, cfg)

	logN(logger, 50, "Job errored; retrying", "job_kind", "feedback_embedding")

	assert.Len(t, inner.records, cfg.First)
}

// One failing job kind must not use up another kind's budget: a storm in embeddings would otherwise
// hide the first failures of webhooks.
func TestSamplingHandlerKeysAreIndependent(t *testing.T) {
	logger, inner, _ := newTestSampler(t, testSampling)

	logN(logger, 20, "Job errored; retrying", "job_kind", "feedback_embedding")

	before := len(inner.records)

	logN(logger, 3, "Job errored; retrying", "job_kind", "webhook_dispatch")
	logN(logger, 3, "Job errored", "job_kind", "feedback_embedding")
	logger.Warn("Job errored; retrying", "job_kind", "feedback_embedding")

	assert.Len(t, inner.records, before+7,
		"another kind, another message and another level are each their own key")
}

func TestSamplingHandlerReportsSuppressedLines(t *testing.T) {
	logger, inner, clock := newTestSampler(t, testSampling)

	logN(logger, 20, "Job errored; retrying", "job_kind", "feedback_embedding", "job_id", 1)
	require.Len(t, inner.records, 6)

	clock.Advance(testSampling.Window)
	logger.Info("Job errored; retrying", "job_kind", "feedback_embedding", "job_id", 2)

	require.Len(t, inner.records, 8, "a summary line, then the line that opened the new window")
	assert.Equal(t, suppressedMessage, inner.records[6].Message)
	assert.Equal(t, slog.LevelInfo, inner.records[6].Level, "the summary keeps the suppressed lines' level")
	assert.Equal(t, "Job errored; retrying", inner.attr(6, "suppressed_message").String())
	assert.Equal(t, "feedback_embedding", inner.attr(6, "job_kind").String())
	assert.Equal(t, int64(14), inner.attr(6, "suppressed").Int64())
	assert.Equal(t, "Job errored; retrying", inner.records[7].Message)

	// The new window starts with a fresh budget, and no second summary until it drops something.
	logN(logger, testSampling.First-1, "Job errored; retrying", "job_kind", "feedback_embedding")
	assert.Len(t, inner.records, 8+testSampling.First-1)
}

func TestSamplingHandlerNoSummaryWithoutDrops(t *testing.T) {
	logger, inner, clock := newTestSampler(t, testSampling)

	logN(logger, testSampling.First, "Job errored; retrying", "job_kind", "feedback_embedding")
	clock.Advance(testSampling.Window)
	logN(logger, testSampling.First, "Job errored; retrying", "job_kind", "feedback_embedding")

	assert.NotContains(t, inner.messages(), suppressedMessage)
}

func TestSamplingHandlerDerivedHandlersShareTheBudget(t *testing.T) {
	logger, inner, _ := newTestSampler(t, testSampling)

	derived := logger.With("component", "river").WithGroup("g")

	logN(logger, testSampling.First, "Job errored; retrying", "job_kind", "feedback_embedding")
	logN(derived, 4, "Job errored; retrying", "job_kind", "feedback_embedding")

	assert.Len(t, inner.records, testSampling.First,
		"a derived logger must not get a fresh budget, or With() would bypass sampling")
}

func TestSamplingHandlerBoundsTrackedKeys(t *testing.T) {
	logger, _, _ := newTestSampler(t, testSampling)

	for i := range samplingMaxKeys * 3 {
		logger.Info("Job errored", "job_kind", fmt.Sprintf("kind-%d", i))
	}

	handler, ok := logger.Handler().(*SamplingHandler)
	require.True(t, ok)
	assert.LessOrEqual(t, len(handler.sampler.counters), samplingMaxKeys)
}

func TestSamplingHandlerIsSafeForConcurrentUse(t *testing.T) {
	logger, inner, _ := newTestSampler(t, testSampling)

	const goroutines, perGoroutine = 8, 250

	var loggers sync.WaitGroup

	for range goroutines {
		loggers.Go(func() {
			logN(logger, perGoroutine, "Job errored; retrying", "job_kind", "feedback_embedding")
		})
	}

	loggers.Wait()

	total := goroutines * perGoroutine
	want := testSampling.First + (total-testSampling.First)/testSampling.Thereafter
	assert.Len(t, inner.records, want, "the count must not drift under concurrent logging")
}

func TestNewSamplingHandlerWithoutWindowIsPassThrough(t *testing.T) {
	inner := &recordingHandler{}

	assert.Same(t, inner, NewSamplingHandler(inner, LogSamplingConfig{First: 1}))
}

func TestSamplingHandlerRespectsInnerLevel(t *testing.T) {
	handler := NewSamplingHandler(slog.NewJSONHandler(nil, &slog.HandlerOptions{Level: slog.LevelWarn}), testSampling)

	assert.False(t, handler.Enabled(context.Background(), slog.LevelInfo))
	assert.True(t, handler.Enabled(context.Background(), slog.LevelError))
}
