package service

import (
	"sync"
	"time"
)

// RateLimiter enforces rate limiting for polling connectors
type RateLimiter struct {
	minDelay         time.Duration
	maxPollsPerHour  int
	pollTimestamps   map[string][]time.Time
	pollTimestampsMu sync.RWMutex
	cleanupInterval  time.Duration
	lastCleanup      time.Time
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(minDelay time.Duration, maxPollsPerHour int) *RateLimiter {
	rl := &RateLimiter{
		minDelay:        minDelay,
		maxPollsPerHour: maxPollsPerHour,
		pollTimestamps:  make(map[string][]time.Time),
		cleanupInterval: 1 * time.Hour,
		lastCleanup:     time.Now(),
	}
	return rl
}

// CanPoll checks if a poll is allowed for the given instance ID
func (rl *RateLimiter) CanPoll(instanceID string) (bool, time.Duration) {
	rl.pollTimestampsMu.Lock()
	defer rl.pollTimestampsMu.Unlock()

	// Cleanup old timestamps periodically
	now := time.Now()
	if now.Sub(rl.lastCleanup) > rl.cleanupInterval {
		rl.cleanupOldTimestamps(now)
		rl.lastCleanup = now
	}

	timestamps := rl.pollTimestamps[instanceID]

	// Check minimum delay
	if len(timestamps) > 0 {
		lastPoll := timestamps[len(timestamps)-1]
		timeSinceLastPoll := now.Sub(lastPoll)
		if timeSinceLastPoll < rl.minDelay {
			waitTime := rl.minDelay - timeSinceLastPoll
			return false, waitTime
		}
	}

	// Check max polls per hour
	oneHourAgo := now.Add(-1 * time.Hour)
	recentPolls := 0
	for _, ts := range timestamps {
		if ts.After(oneHourAgo) {
			recentPolls++
		}
	}

	if recentPolls >= rl.maxPollsPerHour {
		// Find the oldest poll in the last hour to calculate wait time
		if len(timestamps) > 0 {
			oldestRecentPoll := timestamps[0]
			for _, ts := range timestamps {
				if ts.After(oneHourAgo) && ts.Before(oldestRecentPoll) {
					oldestRecentPoll = ts
				}
			}
			waitTime := oldestRecentPoll.Add(1 * time.Hour).Sub(now)
			if waitTime > 0 {
				return false, waitTime
			}
		}
		return false, rl.minDelay
	}

	return true, 0
}

// RecordPoll records a poll timestamp for the given instance ID
func (rl *RateLimiter) RecordPoll(instanceID string) {
	rl.pollTimestampsMu.Lock()
	defer rl.pollTimestampsMu.Unlock()

	now := time.Now()
	rl.pollTimestamps[instanceID] = append(rl.pollTimestamps[instanceID], now)
}

// cleanupOldTimestamps removes timestamps older than 1 hour
func (rl *RateLimiter) cleanupOldTimestamps(now time.Time) {
	oneHourAgo := now.Add(-1 * time.Hour)
	for instanceID, timestamps := range rl.pollTimestamps {
		filtered := make([]time.Time, 0)
		for _, ts := range timestamps {
			if ts.After(oneHourAgo) {
				filtered = append(filtered, ts)
			}
		}
		if len(filtered) == 0 {
			delete(rl.pollTimestamps, instanceID)
		} else {
			rl.pollTimestamps[instanceID] = filtered
		}
	}
}
