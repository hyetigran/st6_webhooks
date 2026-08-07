package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"webhooks-go/internal/testsupport"
	"webhooks-go/internal/worker"
)

var (
	replayWindowStart  = time.Now().Add(-60 * time.Second)
	replayWindowEnd    = time.Now().Add(60 * time.Second)
	replayBeforeWindow = time.Now().Add(-120 * time.Second)
)

func insertReplay(t *testing.T, pool *pgxpool.Pool, endpointID string, rangeStart, rangeEnd time.Time) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO replays (endpoint_id, idempotency_key, range_start, range_end) VALUES ($1, $2, $3, $4) RETURNING id`,
		endpointID, "replay-fixture-"+uuid.NewString(), rangeStart, rangeEnd,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestRunReplayExpansionCycleCreatesFreshDeliveryPerTerminalOriginal(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	originalID, originalEventID := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{
		State: "succeeded", CreatedAt: &replayWindowStart,
	})
	replayID := insertReplay(t, pool, endpointID, replayWindowStart, replayWindowEnd)

	did, err := worker.RunReplayExpansionCycle(ctx, pool)
	require.NoError(t, err)
	require.True(t, did)

	var status string
	require.NoError(t, pool.QueryRow(ctx, "SELECT status FROM replays WHERE id = $1", replayID).Scan(&status))
	require.Equal(t, "expanded", status)

	rows, err := pool.Query(ctx, "SELECT id, event_id, state FROM deliveries WHERE endpoint_id = $1 AND id != $2", endpointID, originalID)
	require.NoError(t, err)
	var replayed []struct {
		ID      string
		EventID string
		State   string
	}
	for rows.Next() {
		var r struct {
			ID      string
			EventID string
			State   string
		}
		require.NoError(t, rows.Scan(&r.ID, &r.EventID, &r.State))
		replayed = append(replayed, r)
	}
	require.Len(t, replayed, 1)
	require.Equal(t, originalEventID, replayed[0].EventID)
	require.Equal(t, "pending", replayed[0].State)
	require.NotEqual(t, originalID, replayed[0].ID) // fresh delivery_id, per R-19/R-20
}

func TestRunReplayExpansionCycleExcludesOriginalsOutsideWindow(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{
		State: "succeeded", CreatedAt: &replayBeforeWindow,
	})
	insertReplay(t, pool, endpointID, replayWindowStart, replayWindowEnd)

	_, err := worker.RunReplayExpansionCycle(ctx, pool)
	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM deliveries WHERE endpoint_id = $1 AND state != 'succeeded'", endpointID).Scan(&count))
	require.Equal(t, 0, count)
}

func TestRunReplayExpansionCycleExcludesPendingAndInFlightOriginals(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "pending", CreatedAt: &replayWindowStart})
	testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "in_flight", CreatedAt: &replayWindowStart})
	insertReplay(t, pool, endpointID, replayWindowStart, replayWindowEnd)

	_, err := worker.RunReplayExpansionCycle(ctx, pool)
	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM deliveries WHERE endpoint_id = $1", endpointID).Scan(&count))
	require.Equal(t, 2, count) // only the two originals, no replayed rows added
}

func TestRunReplayExpansionCycleInsertsInOriginalChronologicalOrder(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	_, firstEventID := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "succeeded", CreatedAt: &replayWindowStart})
	_, secondEventID := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: "failed", CreatedAt: &replayWindowStart})
	insertReplay(t, pool, endpointID, replayWindowStart, replayWindowEnd)

	_, err := worker.RunReplayExpansionCycle(ctx, pool)
	require.NoError(t, err)

	rows, err := pool.Query(ctx, "SELECT event_id FROM deliveries WHERE endpoint_id = $1 AND state = 'pending' ORDER BY seq", endpointID)
	require.NoError(t, err)
	var eventIDs []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		eventIDs = append(eventIDs, id)
	}
	require.Equal(t, []string{firstEventID, secondEventID}, eventIDs)
}

func TestRunReplayExpansionCycleReturnsFalseWhenNothingToExpand(t *testing.T) {
	pool := testsupport.SetupPool(t)
	did, err := worker.RunReplayExpansionCycle(context.Background(), pool)
	require.NoError(t, err)
	require.False(t, did)
}
