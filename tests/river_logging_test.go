package tests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/pkg/database"
)

// riverLoggingFailArgs is a job kind that exists only in this test, so its client never works a
// job another test inserted.
type riverLoggingFailArgs struct {
	Marker string `json:"marker"`
}

func (riverLoggingFailArgs) Kind() string { return "test_river_logging_always_fails" }

type riverLoggingFailWorker struct {
	river.WorkerDefaults[riverLoggingFailArgs]
}

func (riverLoggingFailWorker) Work(_ context.Context, job *river.Job[riverLoggingFailArgs]) error {
	return fmt.Errorf("simulated failure on attempt %d", job.Attempt)
}

// immediateRetryPolicy keeps the test fast: River's default backoff waits seconds between attempts.
type immediateRetryPolicy struct{}

func (immediateRetryPolicy) NextRetry(*rivertype.JobRow) time.Time { return time.Now() }

// lockedBuffer is a log sink that River's goroutines can write to while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log buffer: %w", err)
	}

	return n, nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// runFailingRiverJob runs one job that fails on every attempt through a River client logging to
// the Hub's own handler at logLevel in JSON mode, waits until River has given up on it, and returns
// the job and every line River logged.
func runFailingRiverJob(t *testing.T, logLevel string, maxAttempts int) (*rivertype.JobRow, []map[string]any) {
	t.Helper()

	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)
	t.Cleanup(db.Close)

	// A queue of its own, so this client works only the job inserted below.
	queue := "test_river_logging_" + strings.ReplaceAll(uuid.NewString(), "-", "")

	riverWorkers := river.NewWorkers()
	river.AddWorker(riverWorkers, riverLoggingFailWorker{})

	logs := &lockedBuffer{}

	client, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		// The handler observability.SetupLogging installs for hub-worker and hub-api.
		Logger:      slog.New(observability.NewLogHandler(logs, logLevel, "json")),
		Queues:      map[string]river.QueueConfig{queue: {MaxWorkers: 1}},
		Workers:     riverWorkers,
		RetryPolicy: immediateRetryPolicy{},
		// River's minimum (it must not undercut FetchCooldown), so the immediate retries run promptly.
		FetchPollInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	failed, cancelSubscription := client.Subscribe(river.EventKindJobFailed)
	t.Cleanup(cancelSubscription)

	require.NoError(t, client.Start(ctx))

	stopped := false

	stop := func() {
		if stopped {
			return
		}

		stopped = true

		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		require.NoError(t, client.Stop(stopCtx))
	}
	t.Cleanup(stop)

	inserted, err := client.Insert(ctx, riverLoggingFailArgs{Marker: queue}, &river.InsertOpts{
		Queue:       queue,
		MaxAttempts: maxAttempts,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM river_job WHERE id = $1`, inserted.Job.ID)
	})

	var last *rivertype.JobRow

	deadline := time.After(30 * time.Second)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case event := <-failed:
			require.Equal(t, inserted.Job.ID, event.Job.ID, "only this test's job runs on its queue")
			last = event.Job
		case <-deadline:
			t.Fatalf("timed out waiting for failed attempt %d of %d", attempt, maxAttempts)
		}
	}

	require.Equal(t, rivertype.JobStateDiscarded, last.State, "the job must have run out of attempts")

	// Stop before reading, so no River goroutine is still mid-write.
	stop()

	var lines []map[string]any

	scanner := bufio.NewScanner(strings.NewReader(logs.String()))
	for scanner.Scan() {
		var line map[string]any

		require.NoError(t, json.Unmarshal(scanner.Bytes(), &line),
			"every River line must be JSON in LOG_FORMAT=json, got %q", scanner.Text())

		lines = append(lines, line)
	}

	require.NoError(t, scanner.Err())

	return last, lines
}

// jobLines returns the lines River logged about one job.
func jobLines(lines []map[string]any, jobID int64) []map[string]any {
	var matched []map[string]any

	for _, line := range lines {
		// encoding/json decodes every number into a float64.
		if id, ok := line["job_id"].(float64); ok && int64(id) == jobID {
			matched = append(matched, line)
		}
	}

	return matched
}

// TestRiverLogsFailedAttemptsThroughHubHandler pins the River contract ENG-2485 relies on. Given the
// Hub's handler as river.Config.Logger, every failed attempt must produce a structured line, at a
// level the default LOG_LEVEL=info lets through, carrying the job id, kind and error. Before the fix
// hub-worker left Logger nil, River fell back to a private WARN-level logger, and the INFO lines it
// writes for failed attempts were filtered out, so a failing job left nothing in the logs.
//
// If a River upgrade moves these lines below INFO or drops the fields, this fails, and the logging
// decision in cmd/worker's newRiverConfig needs revisiting.
func TestRiverLogsFailedAttemptsThroughHubHandler(t *testing.T) {
	const maxAttempts = 2

	job, lines := runFailingRiverJob(t, "info", maxAttempts)

	failures := jobLines(lines, job.ID)
	require.Len(t, failures, maxAttempts, "one line per failed attempt; got lines: %v", lines)

	for i, line := range failures {
		attempt := i + 1

		level, err := parseSlogLevel(line[slog.LevelKey])
		require.NoError(t, err)
		assert.GreaterOrEqual(t, level, slog.LevelInfo,
			"attempt %d: below INFO, the default LOG_LEVEL hides it again", attempt)

		assert.Equal(t, job.Kind, line["job_kind"], "attempt %d", attempt)
		assert.Equal(t, fmt.Sprintf("simulated failure on attempt %d", attempt), line["error"],
			"attempt %d: the line must carry the error the worker returned", attempt)
		assert.NotEmpty(t, line[slog.TimeKey], "attempt %d", attempt)
		assert.NotEmpty(t, line[slog.MessageKey], "attempt %d", attempt)
	}
}

// River's lines follow LOG_LEVEL like everything else: an operator who raises it to warn gets the
// quieter output they asked for. This is the same filtering that hid failures before the fix, now
// under the operator's control rather than hard-coded by River's fallback logger.
func TestRiverLogsFollowConfiguredLevel(t *testing.T) {
	job, lines := runFailingRiverJob(t, "warn", 1)

	assert.Empty(t, jobLines(lines, job.ID), "LOG_LEVEL=warn must filter River's INFO lines")

	for _, line := range lines {
		level, err := parseSlogLevel(line[slog.LevelKey])
		require.NoError(t, err)
		assert.GreaterOrEqual(t, level, slog.LevelWarn, "line below the configured level: %v", line)
	}
}

func parseSlogLevel(raw any) (slog.Level, error) {
	text, ok := raw.(string)
	if !ok {
		return 0, errors.New("log line has no string level")
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(text)); err != nil {
		return 0, fmt.Errorf("parse level %q: %w", text, err)
	}

	return level, nil
}
