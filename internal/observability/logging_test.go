package observability

import (
	"log/slog"
	"testing"
)

// ParseLogLevel backs LOG_LEVEL for both hub-api and hub-worker, so an unrecognized value must
// degrade to info rather than silencing logs — a config typo that hid worker output would defeat the
// point of honoring the level at all.
func TestParseLogLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug":     slog.LevelDebug,
		"info":      slog.LevelInfo,
		"warn":      slog.LevelWarn,
		"error":     slog.LevelError,
		"DEBUG":     slog.LevelDebug,
		"Warn":      slog.LevelWarn,
		"":          slog.LevelInfo,
		"trace":     slog.LevelInfo,
		"verbose":   slog.LevelInfo,
		"warning":   slog.LevelInfo,
		"not-level": slog.LevelInfo,
	}

	for level, want := range tests {
		t.Run(level, func(t *testing.T) {
			if got := ParseLogLevel(level); got != want {
				t.Fatalf("ParseLogLevel(%q) = %v, want %v", level, got, want)
			}
		})
	}
}
