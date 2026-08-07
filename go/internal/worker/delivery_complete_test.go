package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"webhooks-go/internal/testsupport"
	"webhooks-go/internal/worker"
)

var completeBackoffConfig = worker.BackoffConfig{BaseDelayMs: 1_000, Multiplier: 2, MaxDelayMs: 30_000, MaxAttempts: 6}

func setUpClaimedDelivery(t *testing.T, pool *pgxpool.Pool) (claimed *worker.ClaimedDelivery, endpointID, deliveryID string) {
	t.Helper()
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID = testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	deliveryID, _ = testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{})
	claimed, err := worker.ClaimDelivery(ctx, pool, leaseDurationMs, testsupport.SecretEncryptionKey)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	return claimed, endpointID, deliveryID
}

func intPtr(n int) *int { return &n }

func TestCompleteDeliverySucceedsOn2xx(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	claimed, endpointID, deliveryID := setUpClaimedDelivery(t, pool)

	outcome, err := worker.CompleteDelivery(ctx, pool, claimed,
		worker.AttemptOutcome{ResponseStatus: intPtr(200), ResponseBodyTruncated: "ok", DurationMs: 42},
		completeBackoffConfig,
	)
	require.NoError(t, err)
	require.Equal(t, worker.CompleteOutcomeSucceeded, outcome)

	var state string
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE id = $1", deliveryID).Scan(&state))
	require.Equal(t, "succeeded", state)

	var responseStatus int
	var durationMs int
	require.NoError(t, pool.QueryRow(ctx, "SELECT response_status, duration_ms FROM attempts WHERE id = $1", claimed.AttemptID).Scan(&responseStatus, &durationMs))
	require.Equal(t, 200, responseStatus)
	require.Equal(t, 42, durationMs)

	var busy bool
	var leaseID *string
	var status string
	require.NoError(t, pool.QueryRow(ctx, "SELECT busy, lease_id, status FROM endpoints WHERE id = $1", endpointID).Scan(&busy, &leaseID, &status))
	require.False(t, busy)
	require.Nil(t, leaseID)
	require.Equal(t, "active", status)
}

func TestCompleteDeliveryReschedulesOnNon2xxBelowCeiling(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	claimed, endpointID, deliveryID := setUpClaimedDelivery(t, pool)

	outcome, err := worker.CompleteDelivery(ctx, pool, claimed,
		worker.AttemptOutcome{ResponseStatus: intPtr(500), ResponseBodyTruncated: "server error", DurationMs: 10},
		completeBackoffConfig,
	)
	require.NoError(t, err)
	require.Equal(t, worker.CompleteOutcomeRetrying, outcome)

	var state string
	var nextAttemptAt time.Time
	require.NoError(t, pool.QueryRow(ctx, "SELECT state, next_attempt_at FROM deliveries WHERE id = $1", deliveryID).Scan(&state, &nextAttemptAt))
	require.Equal(t, "pending", state)
	// A generous tolerance, not an exact "now" comparison: full jitter can
	// legitimately draw a delay near 0ms, and comparing a Postgres-written
	// timestamp against this process's own clock a moment later is
	// otherwise racy on exactly that draw — a tight bound would flake on
	// the rare near-zero jitter, not on any real scheduling bug.
	require.False(t, nextAttemptAt.Before(time.Now().Add(-500*time.Millisecond)))

	var busy bool
	var status string
	require.NoError(t, pool.QueryRow(ctx, "SELECT busy, status FROM endpoints WHERE id = $1", endpointID).Scan(&busy, &status))
	require.False(t, busy)
	require.Equal(t, "active", status)
}

func TestCompleteDeliveryTreatsNetworkFailureLikeNon2xx(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	claimed, _, deliveryID := setUpClaimedDelivery(t, pool)

	outcome, err := worker.CompleteDelivery(ctx, pool, claimed,
		worker.AttemptOutcome{DurationMs: 5_000, ErrorClass: "total_timeout"},
		completeBackoffConfig,
	)
	require.NoError(t, err)
	require.Equal(t, worker.CompleteOutcomeRetrying, outcome)

	var state string
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE id = $1", deliveryID).Scan(&state))
	require.Equal(t, "pending", state)
}

func TestCompleteDeliveryHaltsAtAttemptCeiling(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	claimed, endpointID, deliveryID := setUpClaimedDelivery(t, pool)
	atCeiling := *claimed
	atCeiling.AttemptNumber = completeBackoffConfig.MaxAttempts

	outcome, err := worker.CompleteDelivery(ctx, pool, &atCeiling,
		worker.AttemptOutcome{ResponseStatus: intPtr(500), ResponseBodyTruncated: "still failing", DurationMs: 10},
		completeBackoffConfig,
	)
	require.NoError(t, err)
	require.Equal(t, worker.CompleteOutcomeHalted, outcome)

	var state string
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE id = $1", deliveryID).Scan(&state))
	require.Equal(t, "failed", state)

	var status string
	var busy bool
	require.NoError(t, pool.QueryRow(ctx, "SELECT status, busy FROM endpoints WHERE id = $1", endpointID).Scan(&status, &busy))
	require.Equal(t, "halted", status)
	require.False(t, busy)
}

func TestCompleteDeliveryDropsWriteBackWhenLeaseLost(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	claimed, endpointID, deliveryID := setUpClaimedDelivery(t, pool)

	// Simulate another worker reclaiming this endpoint after our lease
	// expired — its lease_id no longer matches what we captured at claim.
	_, err := pool.Exec(ctx, "UPDATE endpoints SET lease_id = gen_random_uuid() WHERE id = $1", endpointID)
	require.NoError(t, err)

	outcome, err := worker.CompleteDelivery(ctx, pool, claimed,
		worker.AttemptOutcome{ResponseStatus: intPtr(200), ResponseBodyTruncated: "ok", DurationMs: 42},
		completeBackoffConfig,
	)
	require.NoError(t, err)
	require.Equal(t, worker.CompleteOutcomeLeaseLost, outcome)

	var state string
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE id = $1", deliveryID).Scan(&state))
	require.Equal(t, "in_flight", state) // our write-back must not have touched it
}
