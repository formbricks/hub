package observability

import (
	"context"
	"errors"
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
	// Only the record's own attributes are read, not ones bound earlier with Logger.With. A record
	// without one of them is keyed on the rest.
	KeyAttrs []string
}

// samplingMaxKeys caps the number of tracked keys, so attribute values from outside the process (a
// job kind read from the database, say) cannot grow the counter map without bound. Reaching it
// reports every key's pending drops and resets the counters, so the worst case is one window's lines
// passing unsampled, never a lost count.
const samplingMaxKeys = 1024

// suppressedMessage is the message of the line a SamplingHandler writes for the lines it dropped.
const suppressedMessage = "similar log lines suppressed by sampling"

// SamplingHandler is a slog.Handler that samples similar lines before passing them to the inner
// handler; see LogSamplingConfig. Lines it drops are not lost silently: when a key's window that
// dropped lines is over, the next line with that key is preceded by one summary line at the same
// level, carrying the suppressed message, the key attributes, the count and the window it covers.
// The summary waits until the key logs again, so a storm's last window is reported late (since and
// until still place it), and Flush reports whatever is pending when the logger is retired. Only a
// crash can lose a count, and that window still passed its first lines.
type SamplingHandler struct {
	inner   slog.Handler
	sampler *logSampler
}

// NewSamplingHandler wraps inner with sampling. A config with no Window passes every line through.
func NewSamplingHandler(inner slog.Handler, cfg LogSamplingConfig) *SamplingHandler {
	if cfg.Window <= 0 {
		return &SamplingHandler{inner: inner}
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

// Handle passes the record to the inner handler unless sampling drops it, first writing any summary
// of dropped lines that is due.
func (h *SamplingHandler) Handle(ctx context.Context, record slog.Record) error {
	if h.sampler == nil {
		if err := h.inner.Handle(ctx, record); err != nil {
			return fmt.Errorf("inner handler: %w", err)
		}

		return nil
	}

	pass, summaries := h.sampler.observe(record.Level, record.Message, h.keyAttrs(record))

	// Detached on purpose: the line that triggers a summary belongs to an unrelated job, and
	// context.WithoutCancel would keep its trace and request ids.
	errs := h.writeSummaries(context.Background(), summaries) //nolint:contextcheck // see above

	if pass {
		if err := h.inner.Handle(ctx, record); err != nil {
			errs = append(errs, fmt.Errorf("inner handler: %w", err))
		}
	}

	return errors.Join(errs...)
}

// Flush writes the summaries of every key's pending drops and clears them, so a storm's last window
// is reported when nothing else would trigger it. Call it when the logger is retired, once nothing
// logs through it any more. Lines logged after Flush are sampled as before.
func (h *SamplingHandler) Flush(ctx context.Context) error {
	if h.sampler == nil {
		return nil
	}

	return errors.Join(h.writeSummaries(ctx, h.sampler.flush())...)
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

// writeSummaries hands summaries to the inner handler, collecting rather than stopping at errors.
func (h *SamplingHandler) writeSummaries(ctx context.Context, summaries []slog.Record) []error {
	var errs []error

	for _, summary := range summaries {
		if err := h.inner.Handle(ctx, summary); err != nil {
			errs = append(errs, fmt.Errorf("write sampling summary: %w", err))
		}
	}

	return errs
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

// sampleCounter is one key's state. level, message and keyAttrs are kept so its drops can be
// reported without the line that would normally trigger the summary (see the key cap).
type sampleCounter struct {
	level    slog.Level
	message  string
	keyAttrs []slog.Attr

	windowStart time.Time
	seen        int
	dropped     int
}

// observe counts one line and reports whether it passes, plus the summaries that are due: the
// previous window's, when this line opens a new one, and every key's pending drops when the key cap
// forces a reset.
func (s *logSampler) observe(level slog.Level, message string, keyAttrs []slog.Attr) (bool, []slog.Record) {
	key := samplingKey(level, message, keyAttrs)
	now := s.now()

	var summaries []slog.Record

	s.mu.Lock()

	counter, ok := s.counters[key]
	if !ok {
		if len(s.counters) >= samplingMaxKeys {
			for _, pending := range s.counters {
				if pending.dropped > 0 {
					summaries = append(summaries, s.summary(pending, now))
				}
			}

			clear(s.counters)
		}

		counter = &sampleCounter{level: level, message: message, keyAttrs: keyAttrs, windowStart: now}
		s.counters[key] = counter
	}

	if now.Sub(counter.windowStart) >= s.cfg.Window {
		if counter.dropped > 0 {
			summaries = append(summaries, s.summary(counter, now))
		}

		counter.windowStart, counter.seen, counter.dropped = now, 0, 0
	}

	counter.seen++

	pass := counter.seen <= s.cfg.First ||
		(s.cfg.Thereafter > 0 && (counter.seen-s.cfg.First)%s.cfg.Thereafter == 0)
	if !pass {
		counter.dropped++
	}

	s.mu.Unlock()

	return pass, summaries
}

// flush returns a summary for every key with pending drops and starts those keys on a fresh window,
// so a later summary for the same key never covers the flushed one's window again.
func (s *logSampler) flush() []slog.Record {
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	var summaries []slog.Record

	for _, counter := range s.counters {
		if counter.dropped > 0 {
			summaries = append(summaries, s.summary(counter, now))
			counter.windowStart, counter.seen, counter.dropped = now, 0, 0
		}
	}

	return summaries
}

// summary builds the line reporting a counter's dropped lines. until is the end of the window the
// drops happened in, or now if that window is still open (a key-cap reset).
func (s *logSampler) summary(counter *sampleCounter, now time.Time) slog.Record {
	until := counter.windowStart.Add(s.cfg.Window)
	if until.After(now) {
		until = now
	}

	record := slog.NewRecord(now, counter.level, suppressedMessage, 0)
	record.AddAttrs(slog.String("suppressed_message", counter.message))
	record.AddAttrs(counter.keyAttrs...)
	record.AddAttrs(
		slog.Int("suppressed", counter.dropped),
		slog.Time("since", counter.windowStart),
		slog.Time("until", until),
	)

	return record
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
