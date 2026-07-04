package workers

import (
	"errors"
	"time"

	"github.com/formbricks/hub/internal/huberrors"
)

const (
	// defaultRateLimitSnooze applies when a provider 429 carried no retry-after hint.
	defaultRateLimitSnooze = 30 * time.Second
	// minRateLimitSnooze / maxRateLimitSnooze bound a single snooze so a tiny or absurd
	// provider hint still yields a sane delay.
	minRateLimitSnooze = 5 * time.Second
	maxRateLimitSnooze = 5 * time.Minute
	// maxRateLimitSnoozeWindow caps how long a job may keep snoozing against a standing rate
	// limit before it is allowed to fail (and be recovered by a later backfill), so a
	// permanently exhausted quota cannot snooze a single job forever.
	maxRateLimitSnoozeWindow = time.Hour
)

// rateLimitSnoozeDelay decides how long to snooze a rate-limited enrichment job (shared by every
// enrichment worker — translation, sentiment, emotions, and embedding all call rate-limited
// providers). A provider 429 surfaces as a *huberrors.RateLimitError; snoozing re-queues the job
// without consuming a retry attempt, so a burst against a rate-limited model defers rather than
// drops work. ok is false when err is not a rate-limit error, or when the job has been snoozing
// past maxRateLimitSnoozeWindow — then it fails normally. Recovery past the window: translation's
// backfill runs automatically; sentiment and emotions are recovered by the one-off
// cmd/backfill-classify (their outputs stay NULL, so they remain backfill targets); embedding's
// backfill covers only records with no vector yet.
func rateLimitSnoozeDelay(err error, jobCreatedAt time.Time) (time.Duration, bool) {
	var rateLimited *huberrors.RateLimitError
	if !errors.As(err, &rateLimited) {
		return 0, false
	}

	elapsed := time.Duration(0)
	if !jobCreatedAt.IsZero() {
		elapsed = time.Since(jobCreatedAt)
		if elapsed >= maxRateLimitSnoozeWindow {
			return 0, false
		}
	}

	delay := rateLimited.RetryAfter

	switch {
	case delay <= 0:
		delay = defaultRateLimitSnooze
	case delay < minRateLimitSnooze:
		delay = minRateLimitSnooze
	case delay > maxRateLimitSnooze:
		delay = maxRateLimitSnooze
	}

	// Stop snoozing if the chosen delay would push the job past the window — otherwise a job
	// still inside the window could overshoot the cap by up to maxRateLimitSnooze, breaking the
	// "cannot snooze forever" guarantee and delaying the normal-failure/backfill path.
	if !jobCreatedAt.IsZero() && elapsed+delay > maxRateLimitSnoozeWindow {
		return 0, false
	}

	return delay, true
}
