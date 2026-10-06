package observability

import (
	"context"
	"errors"
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

	handler := NewSamplingHandler(inner, cfg)
	require.NotNil(t, handler.sampler, "a config with a window must sample")

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

	windowStart := clock.Now()

	logN(logger, 20, "Job errored; retrying", "job_kind", "feedback_embedding", "job_id", 1)
	require.Len(t, inner.records, 6)

	clock.Advance(testSampling.Window + time.Second)
	logger.Info("Job errored; retrying", "job_kind", "feedback_embedding", "job_id", 2)

	require.Len(t, inner.records, 8, "a summary line, then the line that opened the new window")
	assert.Equal(t, suppressedMessage, inner.records[6].Message)
	assert.Equal(t, slog.LevelInfo, inner.records[6].Level, "the summary keeps the suppressed lines' level")
	assert.Equal(t, "Job errored; retrying", inner.attr(6, "suppressed_message").String())
	assert.Equal(t, "feedback_embedding", inner.attr(6, "job_kind").String())
	assert.Equal(t, int64(14), inner.attr(6, "suppressed").Int64())
	assert.Equal(t, windowStart, inner.attr(6, "since").Time(), "since is when the sampled window opened")
	assert.Equal(t, windowStart.Add(testSampling.Window), inner.attr(6, "until").Time(),
		"until is when the sampled window closed, not when the summary happened to be written")
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

// Reaching the key cap must not lose drop counts: every line is either written or counted in a
// summary, across the reset too.
func TestSamplingHandlerKeyCapKeepsTheAccounting(t *testing.T) {
	logger, inner, _ := newTestSampler(t, testSampling)

	const storm = 20

	logN(logger, storm, "Job errored; retrying", "job_kind", "feedback_embedding")

	for i := range samplingMaxKeys {
		logger.Info("Job errored", "job_kind", fmt.Sprintf("kind-%d", i))
	}

	written, suppressed := 0, int64(0)

	for i, record := range inner.records {
		if record.Message == suppressedMessage {
			suppressed += inner.attr(i, "suppressed").Int64()

			continue
		}

		written++
	}

	assert.Equal(t, int64(storm+samplingMaxKeys), int64(written)+suppressed,
		"every line must be written or reported as suppressed")
	assert.Positive(t, suppressed, "the storm's drops must be reported when the reset discards its counter")
}

// failingSummaryHandler fails to write summary lines and records everything else.
type failingSummaryHandler struct {
	recordingHandler
}

func (h *failingSummaryHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == suppressedMessage {
		return errors.New("sink unavailable")
	}

	return h.recordingHandler.Handle(ctx, record)
}

func TestSamplingHandlerStillWritesTheRecordWhenTheSummaryFails(t *testing.T) {
	inner := &failingSummaryHandler{}
	clock := &fakeClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}

	handler := NewSamplingHandler(inner, testSampling)
	handler.sampler.now = clock.Now

	record := func() slog.Record {
		r := slog.NewRecord(clock.Now(), slog.LevelInfo, "Job errored; retrying", 0)
		r.AddAttrs(slog.String("job_kind", "feedback_embedding"))

		return r
	}

	for range 20 {
		require.NoError(t, handler.Handle(context.Background(), record()))
	}

	clock.Advance(testSampling.Window)

	err := handler.Handle(context.Background(), record())

	require.Error(t, err, "the summary failure must be reported")
	assert.Len(t, inner.records, 7, "the line that passed sampling must still be written")
}

// contextCapturingHandler records the request id each record was handled with.
type contextCapturingHandler struct {
	recordingHandler

	requestIDs []string
}

func (h *contextCapturingHandler) Handle(ctx context.Context, record slog.Record) error {
	h.requestIDs = append(h.requestIDs, RequestIDFromContext(ctx))

	return h.recordingHandler.Handle(ctx, record)
}

// A summary describes many earlier lines, so it must not carry the trace or request ids of the
// unrelated line that happened to trigger it.
func TestSamplingHandlerSummaryDoesNotInheritTheTriggeringContext(t *testing.T) {
	inner := &contextCapturingHandler{}
	clock := &fakeClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}

	handler := NewSamplingHandler(inner, testSampling)
	handler.sampler.now = clock.Now
	logger := slog.New(handler)

	logN(logger, 20, "Job errored; retrying", "job_kind", "feedback_embedding")
	clock.Advance(testSampling.Window)

	ctx := context.WithValue(context.Background(), RequestIDKey, "request-of-job-21")
	logger.InfoContext(ctx, "Job errored; retrying", "job_kind", "feedback_embedding")

	require.Len(t, inner.records, 8)
	assert.Equal(t, suppressedMessage, inner.records[6].Message)
	assert.Empty(t, inner.requestIDs[6], "the summary must not carry the triggering line's request id")
	assert.Equal(t, "request-of-job-21", inner.requestIDs[7], "the line itself keeps its context")
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
	handler := NewSamplingHandler(inner, LogSamplingConfig{First: 1})

	logN(slog.New(handler), 50, "Job errored; retrying", "job_kind", "feedback_embedding")

	assert.Len(t, inner.records, 50, "no window means no sampling")
	require.NoError(t, handler.Flush(context.Background()))
	assert.Len(t, inner.records, 50, "and nothing to flush")
}

// Flush reports the drops nothing else would: a storm's last window, when the key never logs again.
// With it, every line is written or counted.
func TestSamplingHandlerFlushReportsTheLastWindow(t *testing.T) {
	logger, inner, _ := newTestSampler(t, testSampling)

	const storm = 20

	logN(logger, storm, "Job errored; retrying", "job_kind", "feedback_embedding")

	handler, ok := logger.Handler().(*SamplingHandler)
	require.True(t, ok)
	require.NoError(t, handler.Flush(context.Background()))

	require.Len(t, inner.records, 7, "the six that passed, then one summary")
	assert.Equal(t, suppressedMessage, inner.records[6].Message)
	assert.Equal(t, int64(storm-6), inner.attr(6, "suppressed").Int64())

	require.NoError(t, handler.Flush(context.Background()))
	assert.Len(t, inner.records, 7, "a second flush has nothing left to report")
}

// A flush must not make the next window report the same drops again.
func TestSamplingHandlerFlushDoesNotDoubleReport(t *testing.T) {
	logger, inner, clock := newTestSampler(t, testSampling)

	logN(logger, 20, "Job errored; retrying", "job_kind", "feedback_embedding")

	handler, ok := logger.Handler().(*SamplingHandler)
	require.True(t, ok)
	require.NoError(t, handler.Flush(context.Background()))

	clock.Advance(testSampling.Window)
	logger.Info("Job errored; retrying", "job_kind", "feedback_embedding")

	summaries := 0

	for _, message := range inner.messages() {
		if message == suppressedMessage {
			summaries++
		}
	}

	assert.Equal(t, 1, summaries)
}

func TestSamplingHandlerRespectsInnerLevel(t *testing.T) {
	handler := NewSamplingHandler(slog.NewJSONHandler(nil, &slog.HandlerOptions{Level: slog.LevelWarn}), testSampling)

	assert.False(t, handler.Enabled(context.Background(), slog.LevelInfo))
	assert.True(t, handler.Enabled(context.Background(), slog.LevelError))
}
