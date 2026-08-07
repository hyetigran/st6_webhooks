package worker_test

import (
	"context"
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/properties"
	"webhooks-go/internal/testsupport"
	"webhooks-go/internal/worker"
)

// REVIEW.md F-1 / docs/adr/0001: per-endpoint delivery order must equal
// events.seq order, even when expansion runs fully concurrently — the
// per-tenant advisory lock exists specifically to make this true under
// concurrent workers, not just under the sequential expansion the earlier
// example-based tests (expansion_test.go) exercise.
func TestExpansionOrderPropertyHoldsUnderConcurrentWorkers(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()

	seed := properties.GetTestSeed("expansion-order")
	rng := rand.New(rand.NewSource(seed))
	eventCount := 5 + rng.Intn(11) // [5, 15]
	concurrency := 2 + rng.Intn(4) // [2, 5]

	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})

	publishedEventIDs := make([]string, eventCount)
	for i := 0; i < eventCount; i++ {
		publishedEventIDs[i] = testsupport.CreateEvent(t, pool, tenantID, testsupport.EventOptions{Status: "pending_expansion"})
	}

	// Drains every pending_expansion event by racing several concurrent
	// workers against the real advisory-lock claim — the actual
	// concurrency-safety property under test, not simulated.
	for {
		var wg sync.WaitGroup
		results := make([]bool, concurrency)
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				did, err := worker.RunExpansionCycle(ctx, pool)
				require.NoError(t, err)
				results[i] = did
			}(i)
		}
		wg.Wait()
		anyWork := false
		for _, did := range results {
			anyWork = anyWork || did
		}
		if !anyWork {
			break
		}
	}

	rows, err := pool.Query(ctx, "SELECT event_id FROM deliveries WHERE endpoint_id = $1 ORDER BY seq", endpointID)
	require.NoError(t, err)
	defer rows.Close()
	var deliveredEventIDs []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		deliveredEventIDs = append(deliveredEventIDs, id)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, publishedEventIDs, deliveredEventIDs)
}
