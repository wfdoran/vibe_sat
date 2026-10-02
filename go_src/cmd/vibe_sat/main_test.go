package main

import (
	"math"
	"testing"
	"time"
)

// TestTimeLimitDuration verifies ordinary values convert exactly and
// values too large for a time.Duration saturate instead of wrapping
// negative.
func TestTimeLimitDuration(t *testing.T) {
	if got := timeLimitDuration(30); got != 30*time.Second {
		t.Errorf("timeLimitDuration(30) = %v, want 30s", got)
	}
	for _, secs := range []int{math.MaxInt, 10_000_000_000, math.MaxInt64/int(time.Second) + 1} {
		if got := timeLimitDuration(secs); got <= 0 {
			t.Errorf("timeLimitDuration(%d) = %v, want a positive saturated duration", secs, got)
		}
	}
}
