package worker_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/worker"
)

var backoffConfig = worker.BackoffConfig{BaseDelayMs: 1_000, Multiplier: 2, MaxDelayMs: 30_000, MaxAttempts: 6}

func TestComputeBackoffDelayMsMinimumJitterIsZero(t *testing.T) {
	zero := func() float64 { return 0 }
	require.Equal(t, 0, worker.ComputeBackoffDelayMs(1, backoffConfig, zero))
	require.Equal(t, 0, worker.ComputeBackoffDelayMs(3, backoffConfig, zero))
}

func TestComputeBackoffDelayMsCeilingDoublesEachAttempt(t *testing.T) {
	half := func() float64 { return 0.5 }
	require.Equal(t, 500, worker.ComputeBackoffDelayMs(1, backoffConfig, half))   // ceiling 1000
	require.Equal(t, 1_000, worker.ComputeBackoffDelayMs(2, backoffConfig, half)) // ceiling 2000
	require.Equal(t, 2_000, worker.ComputeBackoffDelayMs(3, backoffConfig, half)) // ceiling 4000
	require.Equal(t, 8_000, worker.ComputeBackoffDelayMs(5, backoffConfig, half)) // ceiling 16000
}

func TestComputeBackoffDelayMsCapsAtMaxDelayMs(t *testing.T) {
	half := func() float64 { return 0.5 }
	// Uncapped ceiling at attempt 10 would be 1000 * 2^9 = 512000ms.
	require.Equal(t, 15_000, worker.ComputeBackoffDelayMs(10, backoffConfig, half)) // half of the 30000ms cap
}
