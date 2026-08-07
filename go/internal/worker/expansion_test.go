package worker_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/testsupport"
	"webhooks-go/internal/worker"
)

func TestRunExpansionCycleCreatesOneDeliveryPerSubscribedEndpoint(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointA := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, "")
	endpointB := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, "")
	eventID := publishEvent(t, pool, tenantID, "order.created", "key-1")

	did, err := worker.RunExpansionCycle(ctx, pool)
	require.NoError(t, err)
	require.True(t, did)

	var status string
	require.NoError(t, pool.QueryRow(ctx, "SELECT status FROM events WHERE id = $1", eventID).Scan(&status))
	require.Equal(t, "expanded", status)

	rows, err := pool.Query(ctx, "SELECT endpoint_id FROM deliveries WHERE event_id = $1", eventID)
	require.NoError(t, err)
	var endpointIDs []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		endpointIDs = append(endpointIDs, id)
	}
	require.ElementsMatch(t, []string{endpointA, endpointB}, endpointIDs)
}

func TestRunExpansionCycleFlipsStatusEvenWithNoSubscribers(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	testsupport.CreateEndpoint(t, pool, tenantID, []string{"payment.failed"}, "") // not subscribed to order.created
	eventID := publishEvent(t, pool, tenantID, "order.created", "key-2")

	did, err := worker.RunExpansionCycle(ctx, pool)
	require.NoError(t, err)
	require.True(t, did)

	var status string
	require.NoError(t, pool.QueryRow(ctx, "SELECT status FROM events WHERE id = $1", eventID).Scan(&status))
	require.Equal(t, "expanded", status)

	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM deliveries WHERE event_id = $1", eventID).Scan(&count))
	require.Equal(t, 0, count)
}

func TestRunExpansionCycleExpandsInPublishOrderAcrossSeparateCalls(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, "")
	firstEventID := publishEvent(t, pool, tenantID, "order.created", "seq-key-1")
	secondEventID := publishEvent(t, pool, tenantID, "order.created", "seq-key-2")

	did1, err := worker.RunExpansionCycle(ctx, pool)
	require.NoError(t, err)
	require.True(t, did1)
	did2, err := worker.RunExpansionCycle(ctx, pool)
	require.NoError(t, err)
	require.True(t, did2)

	rows, err := pool.Query(ctx, "SELECT event_id FROM deliveries WHERE endpoint_id = $1 ORDER BY seq", endpointID)
	require.NoError(t, err)
	var eventIDs []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		eventIDs = append(eventIDs, id)
	}
	require.Equal(t, []string{firstEventID, secondEventID}, eventIDs)
}

func TestRunExpansionCycleQueuesDeliveriesForPausedAndHaltedEndpoints(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	pausedEndpoint := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, "paused")
	haltedEndpoint := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, "halted")
	eventID := publishEvent(t, pool, tenantID, "order.created", "key-status-test")

	did, err := worker.RunExpansionCycle(ctx, pool)
	require.NoError(t, err)
	require.True(t, did)

	rows, err := pool.Query(ctx, "SELECT endpoint_id FROM deliveries WHERE event_id = $1", eventID)
	require.NoError(t, err)
	var endpointIDs []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		endpointIDs = append(endpointIDs, id)
	}
	require.ElementsMatch(t, []string{pausedEndpoint, haltedEndpoint}, endpointIDs)
}

func TestRunExpansionCycleReturnsFalseWhenNothingToDo(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()

	did, err := worker.RunExpansionCycle(ctx, pool)
	require.NoError(t, err)
	require.False(t, did)
}
