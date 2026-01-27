package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRateLimiter_CanPoll(t *testing.T) {
	t.Run("allows poll when no previous polls", func(t *testing.T) {
		rl := NewRateLimiter(1*time.Minute, 60)
		canPoll, waitTime := rl.CanPoll("instance-1")
		assert.True(t, canPoll)
		assert.Equal(t, time.Duration(0), waitTime)
	})

	t.Run("blocks poll when min delay not met", func(t *testing.T) {
		rl := NewRateLimiter(1*time.Minute, 60)
		rl.RecordPoll("instance-1")

		canPoll, waitTime := rl.CanPoll("instance-1")
		assert.False(t, canPoll)
		assert.Greater(t, waitTime, time.Duration(0))
		assert.LessOrEqual(t, waitTime, 1*time.Minute)
	})

	t.Run("allows poll after min delay", func(t *testing.T) {
		rl := NewRateLimiter(100*time.Millisecond, 60)
		rl.RecordPoll("instance-1")

		time.Sleep(150 * time.Millisecond)

		canPoll, waitTime := rl.CanPoll("instance-1")
		assert.True(t, canPoll)
		assert.Equal(t, time.Duration(0), waitTime)
	})

	t.Run("blocks poll when max polls per hour exceeded", func(t *testing.T) {
		rl := NewRateLimiter(1*time.Millisecond, 3) // Max 3 polls per hour

		// Record 3 polls
		for i := 0; i < 3; i++ {
			rl.RecordPoll("instance-1")
			time.Sleep(2 * time.Millisecond)
		}

		canPoll, _ := rl.CanPoll("instance-1")
		assert.False(t, canPoll)
	})
}

func TestRateLimiter_RecordPoll(t *testing.T) {
	rl := NewRateLimiter(1*time.Minute, 60)

	t.Run("records poll timestamp", func(t *testing.T) {
		rl.RecordPoll("instance-1")

		// Should block immediately after recording
		canPoll, _ := rl.CanPoll("instance-1")
		assert.False(t, canPoll)
	})
}
