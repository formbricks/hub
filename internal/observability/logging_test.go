package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLogHandlerJSONIncludesRequestCorrelation(t *testing.T) {
	var output bytes.Buffer

	logger := slog.New(NewLogHandler(&output, "info", "json"))
	ctx := context.WithValue(context.Background(), RequestIDKey, "request-1")

	logger.InfoContext(ctx, "taxonomy lifecycle", "event", "hub.taxonomy.run.started")

	assert.Equal(t, 1, strings.Count(output.String(), `"request_id"`))

	var record map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &record))
	assert.Equal(t, "request-1", record["request_id"])
	assert.Equal(t, "hub.taxonomy.run.started", record["event"])
}

func TestNewLogHandlerDefaultsToText(t *testing.T) {
	var output bytes.Buffer

	logger := slog.New(NewLogHandler(&output, "info", ""))

	logger.Info("taxonomy lifecycle", "event", "hub.taxonomy.run.started")

	assert.True(t, strings.HasPrefix(output.String(), "time="))
	assert.Contains(t, output.String(), "event=hub.taxonomy.run.started")
}
