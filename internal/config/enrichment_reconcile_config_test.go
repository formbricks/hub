package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestEnrichmentReconcileInterval covers the sweep interval's fallback. Both functions here exist
// only to survive misconfiguration, and both were shipped untested -- so the branch that turns a
// typo into a sane default had nothing proving it does.
func TestEnrichmentReconcileInterval(t *testing.T) {
	for name, testCase := range map[string]struct {
		seconds int
		want    time.Duration
	}{
		"configured":         {seconds: 60, want: 60 * time.Second},
		"zero falls back":    {seconds: 0, want: 300 * time.Second},
		"negative is a typo": {seconds: -1, want: 300 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, testCase.want, EnrichmentReconcileConfig{IntervalSeconds: testCase.seconds}.Interval())
		})
	}
}

// TestEnrichmentReconcileDepth covers the target depth's clamp on both sides.
//
// The ceiling is the one that matters operationally: TargetDepth is how many jobs each reconcile
// queue is topped up TO, so an operator who reads it as "records to process" and sets 10000000
// would have the sweep try to materialise that many rows into river_job in one pass. The floor
// catches the same mistake from the other direction, where 0 would otherwise mean "never enqueue
// anything" and coverage would silently stop converging.
func TestEnrichmentReconcileDepth(t *testing.T) {
	for name, testCase := range map[string]struct {
		depth int
		want  int
	}{
		"configured":          {depth: 250, want: 250},
		"zero falls back":     {depth: 0, want: 1000},
		"negative falls back": {depth: -5, want: 1000},
		"at the ceiling":      {depth: 100_000, want: 100_000},
		"above is clamped":    {depth: 10_000_000, want: 100_000},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, testCase.want, EnrichmentReconcileConfig{TargetDepth: testCase.depth}.Depth())
		})
	}
}
