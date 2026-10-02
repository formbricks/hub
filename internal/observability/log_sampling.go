package observability

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// LogSamplingConfig bounds how many similar log lines a SamplingHandler passes through. Lines are
// similar when they share a level, a message and the values of KeyAttrs. Within each Window, the
// first First similar lines pass, then every Thereafter-th; the rest are dropped and counted.
//
// This is the shape of zap's production sampler: it caps a storm of identical lines (a provider
// outage failing every job attempt) while leaving ordinary traffic untouched, since a key below
// First lines per window is never sampled.
type LogSamplingConfig struct {
	// Window is the counting interval. Zero or negative disables sampling.
	Window time.Duration
	// First is how many similar lines pass in each window before sampling starts.
	First int
	// Thereafter passes every Thereafter-th similar line after the first First. Zero or negative
	// drops them all.
	Thereafter int
	// KeyAttrs names the record attributes that, with the level and message, identify similar lines.
	// A record without one of them is keyed on the rest.
	KeyAttrs []string
}

// samplingMaxKeys caps the number of tracked keys, so attribute values from outside the process (a
// job kind read from the database, say) cannot grow the counter map without bound. Reaching it
// resets every counter; at worst that lets one window's lines through unsampled.
const samplingMaxKeys = 1024

// suppressedMessage is the message of the line a SamplingHandler writes for the lines it dropped.
const suppressedMessage = "similar log lines suppressed by sampling"

// SamplingHandler is a slog.Handler that samples similar lines before passing them to the inner
// handler; see LogSamplingConfig. Lines it drops are not lost silently: once a window that dropped
// lines is over, the next similar line is preceded by one summary line at the same level, carrying
// the suppressed message, the key attributes and the count. The count for a key's last window is
// only reported if that key logs again; that window still passed its first lines, so the storm itself
// is always visible.
type SamplingHandler struct {
	inner   slog.Handler
	sampler *logSampler
}

// NewSamplingHandler wraps inner with sampling. A config with no Window returns inner unchanged.
func NewSamplingHandler(inner slog.Handler, cfg LogSamplingConfig) slog.Handler {
	if cfg.Window <= 0 {
		return inner
	}

	return &SamplingHandler{
		inner: inner,
		sampler: &logSampler{
			cfg:      cfg,
			now:      time.Now,
			counters: make(map[string]*sampleCounter),
		},
	}
}

// Enabled reports whether the inner handler is enabled for the level. Sampling happens in Handle,
// so a disabled level costs nothing here.
func (h *SamplingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle passes the record to the inner handler unless sampling drops it, first writing the summary
// of a finished window's dropped lines when there is one.
func (h *SamplingHandler) Handle(ctx context.Context, record slog.Record) error {
	keyAttrs := h.keyAttrs(record)
	pass, summary := h.sampler.observe(record.Level, record.Message, keyAttrs)

	if summary != nil {
		if err := h.inner.Handle(ctx, *summary); err != nil {
			return fmt.Errorf("write sampling summary: %w", err)
		}
	}

	if !pass {
		return nil
	}

	if err := h.inner.Handle(ctx, record); err != nil {
		return fmt.Errorf("inner handler: %w", err)
	}

	return nil
}

// WithAttrs returns a handler with attrs added to the inner handler. It shares this handler's
// counters, so derived loggers are sampled together rather than each getting a fresh budget.
func (h *SamplingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SamplingHandler{inner: h.inner.WithAttrs(attrs), sampler: h.sampler}
}

// WithGroup returns a handler with the group added to the inner handler, sharing the counters.
func (h *SamplingHandler) WithGroup(name string) slog.Handler {
	return &SamplingHandler{inner: h.inner.WithGroup(name), sampler: h.sampler}
}

// keyAttrs returns the record's values for the configured key attributes, in config order.
func (h *SamplingHandler) keyAttrs(record slog.Record) []slog.Attr {
	if len(h.sampler.cfg.KeyAttrs) == 0 {
		return nil
	}

	found := make([]slog.Attr, 0, len(h.sampler.cfg.KeyAttrs))

	for _, name := range h.sampler.cfg.KeyAttrs {
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key != name {
				return true
			}

			found = append(found, attr)

			return false
		})
	}

	return found
}

type logSampler struct {
	cfg LogSamplingConfig
	now func() time.Time

	mu       sync.Mutex
	counters map[string]*sampleCounter
}

type sampleCounter struct {
	windowStart time.Time
	seen        int
	dropped     int
}

// observe counts one line and reports whether it passes, plus a summary record when the line opens
// a new window and the previous one dropped lines.
func (s *logSampler) observe(level slog.Level, message string, keyAttrs []slog.Attr) (bool, *slog.Record) {
	key := samplingKey(level, message, keyAttrs)
	now := s.now()

	s.mu.Lock()

	counter, ok := s.counters[key]
	if !ok {
		if len(s.counters) >= samplingMaxKeys {
			clear(s.counters)
		}

		counter = &sampleCounter{windowStart: now}
		s.counters[key] = counter
	}

	var (
		suppressed int
		since      time.Time
	)

	if now.Sub(counter.windowStart) >= s.cfg.Window {
		suppressed, since = counter.dropped, counter.windowStart
		counter.windowStart, counter.seen, counter.dropped = now, 0, 0
	}

	counter.seen++

	pass := counter.seen <= s.cfg.First ||
		(s.cfg.Thereafter > 0 && (counter.seen-s.cfg.First)%s.cfg.Thereafter == 0)
	if !pass {
		counter.dropped++
	}

	s.mu.Unlock()

	if suppressed == 0 {
		return pass, nil
	}

	summary := slog.NewRecord(now, level, suppressedMessage, 0)
	summary.AddAttrs(slog.String("suppressed_message", message))
	summary.AddAttrs(keyAttrs...)
	summary.AddAttrs(slog.Int("suppressed", suppressed), slog.Time("since", since))

	return pass, &summary
}

// samplingKey joins the level, message and key attribute values with NUL separators. A collision
// needs a NUL inside a log message, and would only merge two keys' sampling budgets.
func samplingKey(level slog.Level, message string, keyAttrs []slog.Attr) string {
	var key strings.Builder

	key.WriteString(level.String())
	key.WriteByte(0)
	key.WriteString(message)

	for _, attr := range keyAttrs {
		key.WriteByte(0)
		key.WriteString(attr.Key)
		key.WriteByte('=')
		key.WriteString(attr.Value.String())
	}

	return key.String()
}
