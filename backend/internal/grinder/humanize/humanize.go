package humanize

import (
	"math/rand"
	"time"
)

// RandDelay returns a random duration in [min, max].
func RandDelay(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	delta := int64(max - min)
	return min + time.Duration(rand.Int63n(delta))
}

// SleepRand sleeps a random duration in [min, max].
func SleepRand(min, max time.Duration) {
	time.Sleep(RandDelay(min, max))
}

// ShiftJitter adds +/- variance to a base duration.
func ShiftJitter(base, variance time.Duration) time.Duration {
	if variance <= 0 {
		return base
	}
	jitter := time.Duration(rand.Int63n(int64(variance*2))) - variance
	out := base + jitter
	if out < 0 {
		out = base
	}
	return out
}
