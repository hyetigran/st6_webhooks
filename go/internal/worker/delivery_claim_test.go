package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/testsupport"
	"webhooks-go/internal/worker"
)

const leaseDurationMs = 60_000

func TestClaimDeliveryClaimsOldestPendingDeliveryAndInsertsAttempt(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{
		URL:           "https://example.com/hook",
		SigningSecret: "whsec_abc",
	})
	deliveryID, eventID := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{
		EventType: "order.created",
		Payload:   `{"orderId":"123"}`,
	})

	claimed, err := worker.ClaimDelivery(ctx, pool, leaseDurationMs, testsupport.SecretEncryptionKey)
	require.NoError(t, err)
	require.NotNil(t, claimed)

	require.Equal(t, endpointID, claimed.EndpointID)
	require.Equal(t, tenantID, claimed.TenantID)
	require.Equal(t, deliveryID, claimed.DeliveryID)
	require.Equal(t, eventID, claimed.EventID)
	require.Equal(t, "order.created", claimed.EventType)
	require.JSONEq(t, `{"orderId":"123"}`, string(claimed.Payload))
	require.Equal(t, 1, claimed.AttemptNumber)
	require.Equal(t, "https://example.com/hook", claimed.URL)
	require.Equal(t, "whsec_abc", claimed.SigningSecret)
	require.Empty(t, claimed.SecondarySecret)
	require.NotEmpty(t, claimed.LeaseID)

	var state string
	var attemptCount int
	require.NoError(t, pool.QueryRow(ctx, "SELECT state, attempt_count FROM deliveries WHERE id = $1", deliveryID).Scan(&state, &attemptCount))
	require.Equal(t, "in_flight", state)
	require.Equal(t, 1, attemptCount)

	var attemptNumber int
	var sentAt *time.Time
	var responseStatus *int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT attempt_number, sent_at, response_status FROM attempts WHERE delivery_id = $1", deliveryID,
	).Scan(&attemptNumber, &sentAt, &responseStatus))
	require.Equal(t, 1, attemptNumber)
	require.NotNil(t, sentAt)
	require.Nil(t, responseStatus)

	var busy bool
	var leaseID string
	require.NoError(t, pool.QueryRow(ctx, "SELECT busy, lease_id FROM endpoints WHERE id = $1", endpointID).Scan(&busy, &leaseID))
	require.True(t, busy)
	require.Equal(t, claimed.LeaseID, leaseID)
}

func TestClaimDeliveryUpdatesTenantLastServedAt(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{})

	_, err := worker.ClaimDelivery(ctx, pool, leaseDurationMs, testsupport.SecretEncryptionKey)
	require.NoError(t, err)

	var lastServedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx, "SELECT last_served_at FROM tenants WHERE id = $1", tenantID).Scan(&lastServedAt))
	require.NotNil(t, lastServedAt)
}

func TestClaimDeliveryPrefersLongestUnservedTenant(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	quietTenant, _ := testsupport.CreateTenant(t, pool)
	servedTenant, _ := testsupport.CreateTenant(t, pool)
	_, err := pool.Exec(ctx, "UPDATE tenants SET last_served_at = now() WHERE id = $1", servedTenant)
	require.NoError(t, err)

	quietEndpoint := testsupport.CreateEndpoint(t, pool, quietTenant, []string{"order.created"}, testsupport.EndpointOptions{})
	servedEndpoint := testsupport.CreateEndpoint(t, pool, servedTenant, []string{"order.created"}, testsupport.EndpointOptions{})
	testsupport.CreateDelivery(t, pool, quietTenant, quietEndpoint, testsupport.DeliveryOptions{})
	testsupport.CreateDelivery(t, pool, servedTenant, servedEndpoint, testsupport.DeliveryOptions{})

	claimed, err := worker.ClaimDelivery(ctx, pool, leaseDurationMs, testsupport.SecretEncryptionKey)
	require.NoError(t, err)
	require.Equal(t, quietEndpoint, claimed.EndpointID)
}

// R-11: found via chaos testing in the Node stack — an earlier version of
// the delivery-pick query filtered on next_attempt_at <= now(), which let
// it skip past an ineligible head to grab a later delivery that happened to
// be ready, violating strict per-endpoint order the moment a head is
// mid-backoff.
func TestClaimDeliveryNeverJumpsAheadOfCoolingDownHead(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	future := time.Now().Add(60 * time.Second)
	headID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{NextAttemptAt: &future})
	followerID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{}) // next_attempt_at defaults to now — immediately eligible

	claimed, err := worker.ClaimDelivery(ctx, pool, leaseDurationMs, testsupport.SecretEncryptionKey)
	require.NoError(t, err)
	require.Nil(t, claimed)

	rows, err := pool.Query(ctx, "SELECT id, state FROM deliveries WHERE id = ANY($1)", []string{headID, followerID})
	require.NoError(t, err)
	for rows.Next() {
		var id, state string
		require.NoError(t, rows.Scan(&id, &state))
		require.Equal(t, "pending", state) // neither was claimed
	}

	// Once the head's cooldown actually elapses, it — not the follower — is
	// what gets claimed next.
	_, err = pool.Exec(ctx, "UPDATE deliveries SET next_attempt_at = now() WHERE id = $1", headID)
	require.NoError(t, err)
	claimedAfterCooldown, err := worker.ClaimDelivery(ctx, pool, leaseDurationMs, testsupport.SecretEncryptionKey)
	require.NoError(t, err)
	require.Equal(t, headID, claimedAfterCooldown.DeliveryID)
}

func TestClaimDeliveryNeverClaimsPausedEndpoint(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{Status: "paused"})
	deliveryID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{})

	claimed, err := worker.ClaimDelivery(ctx, pool, leaseDurationMs, testsupport.SecretEncryptionKey)
	require.NoError(t, err)
	require.Nil(t, claimed)

	var state string
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE id = $1", deliveryID).Scan(&state))
	require.Equal(t, "pending", state) // accumulated, not discarded
}

func TestClaimDeliveryReturnsNilWhenEndpointAlreadyBusyWithinLease(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{})
	_, err := pool.Exec(ctx, "UPDATE endpoints SET busy = true, busy_since = now(), lease_id = gen_random_uuid() WHERE id = $1", endpointID)
	require.NoError(t, err)

	claimed, err := worker.ClaimDelivery(ctx, pool, leaseDurationMs, testsupport.SecretEncryptionKey)
	require.NoError(t, err)
	require.Nil(t, claimed)
}

func TestClaimDeliveryReturnsNilWhenNothingPending(t *testing.T) {
	pool := testsupport.SetupPool(t)
	claimed, err := worker.ClaimDelivery(context.Background(), pool, leaseDurationMs, testsupport.SecretEncryptionKey)
	require.NoError(t, err)
	require.Nil(t, claimed)
}

func TestClaimDeliveryReclaimsStaleLeaseAndClosesOrphanedAttempt(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	deliveryID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{})

	// Simulate a worker that claimed this delivery, sent an attempt, then
	// died before writing back — busy_since is far enough in the past that
	// its lease has expired.
	staleBusySince := time.Now().Add(-2 * leaseDurationMs * time.Millisecond)
	_, err := pool.Exec(ctx, "UPDATE endpoints SET busy = true, busy_since = $2, lease_id = gen_random_uuid() WHERE id = $1", endpointID, staleBusySince)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "UPDATE deliveries SET state = 'in_flight', attempt_count = 1 WHERE id = $1", deliveryID)
	require.NoError(t, err)
	var orphanedAttemptID string
	require.NoError(t, pool.QueryRow(ctx,
		"INSERT INTO attempts (delivery_id, attempt_number, sent_at) VALUES ($1, 1, now() - interval '1 hour') RETURNING id", deliveryID,
	).Scan(&orphanedAttemptID))

	claimed, err := worker.ClaimDelivery(ctx, pool, leaseDurationMs, testsupport.SecretEncryptionKey)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, deliveryID, claimed.DeliveryID)
	require.Equal(t, 2, claimed.AttemptNumber)

	var errorClass *string
	require.NoError(t, pool.QueryRow(ctx, "SELECT error_class FROM attempts WHERE id = $1", orphanedAttemptID).Scan(&errorClass))
	require.NotNil(t, errorClass)
	require.Equal(t, "worker_lease_expired", *errorClass)
}

func TestClaimDeliveryIncludesActiveSecondarySecret(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	future := time.Now().Add(60 * time.Second)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{
		SigningSecret:            "whsec_new",
		SecondarySecret:          "whsec_old",
		SecondarySecretExpiresAt: &future,
	})
	testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{})

	claimed, err := worker.ClaimDelivery(ctx, pool, leaseDurationMs, testsupport.SecretEncryptionKey)
	require.NoError(t, err)
	require.Equal(t, "whsec_new", claimed.SigningSecret)
	require.Equal(t, "whsec_old", claimed.SecondarySecret)
}

func TestClaimDeliveryOmitsExpiredSecondarySecret(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	past := time.Now().Add(-60 * time.Second)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{
		SigningSecret:            "whsec_new",
		SecondarySecret:          "whsec_old",
		SecondarySecretExpiresAt: &past,
	})
	testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{})

	claimed, err := worker.ClaimDelivery(ctx, pool, leaseDurationMs, testsupport.SecretEncryptionKey)
	require.NoError(t, err)
	require.Empty(t, claimed.SecondarySecret)
}

func TestClaimDeliveryClaimsTwoEndpointsConcurrentlyWithoutCollision(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointA := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	endpointB := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	testsupport.CreateDelivery(t, pool, tenantID, endpointA, testsupport.DeliveryOptions{})
	testsupport.CreateDelivery(t, pool, tenantID, endpointB, testsupport.DeliveryOptions{})

	type claimResult struct {
		claimed *worker.ClaimedDelivery
		err     error
	}
	results := make(chan claimResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			claimed, err := worker.ClaimDelivery(ctx, pool, leaseDurationMs, testsupport.SecretEncryptionKey)
			results <- claimResult{claimed, err}
		}()
	}
	first := <-results
	second := <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)

	claimedIDs := []string{first.claimed.EndpointID, second.claimed.EndpointID}
	require.ElementsMatch(t, []string{endpointA, endpointB}, claimedIDs)
}
