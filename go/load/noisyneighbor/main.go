// PRD §8 (R-18): "One tenant floods; a quiet tenant's p99 stays within its
// bound." Exercises the tenant-fairness claim ordering (least-recently-
// served tenant first, docs/adr/0004) under real concurrent load from a
// real worker pool, not a single worker.
package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"webhooks-go/internal/scenariosupport"
)

const (
	workerCount            = 3
	floodEndpointCount     = 20
	floodEventsPerEndpoint = 5
	quietEventCount        = 10
)

func main() {
	scenariosupport.RunScenario("load", "noisy-neighbor", run)
}

func run() (map[string]any, error) {
	ctx := context.Background()
	pool, err := scenariosupport.SetupDatabase(ctx, scenariosupport.LoadDatabaseName, scenariosupport.LoadDatabaseURL)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	port, closeReceiver, err := scenariosupport.StartLocalReceiver(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if err != nil {
		return nil, err
	}
	defer closeReceiver()
	url := fmt.Sprintf("http://load-test.local:%d/hook", port)

	floodTenantID, _, err := scenariosupport.CreateTenant(ctx, pool, "flood-tenant")
	if err != nil {
		return nil, err
	}
	quietTenantID, _, err := scenariosupport.CreateTenant(ctx, pool, "quiet-tenant")
	if err != nil {
		return nil, err
	}

	floodEndpointIDs := make([]string, floodEndpointCount)
	for i := 0; i < floodEndpointCount; i++ {
		id, err := scenariosupport.CreateEndpoint(ctx, pool, floodTenantID, []string{"order.created"}, url, "")
		if err != nil {
			return nil, err
		}
		floodEndpointIDs[i] = id
	}
	quietEndpointID, err := scenariosupport.CreateEndpoint(ctx, pool, quietTenantID, []string{"order.created"}, url, "")
	if err != nil {
		return nil, err
	}

	_, cleanupWorkers, err := scenariosupport.SpawnWorkerPool("./bin/chaosworker", workerCount, map[string]string{
		"DATABASE_URL":                 scenariosupport.LoadDatabaseURL,
		"WORKER_IDLE_POLL_INTERVAL_MS": "20",
	})
	if err != nil {
		return nil, err
	}
	defer cleanupWorkers()

	// Flood: publish a large backlog across many endpoints, all at once,
	// concurrently (this is the noise — we don't wait on it).
	var wg sync.WaitGroup
	var floodErr error
	var floodErrMu sync.Mutex
	for _, endpointID := range floodEndpointIDs {
		for i := 0; i < floodEventsPerEndpoint; i++ {
			wg.Add(1)
			go func(endpointID string) {
				defer wg.Done()
				if _, _, err := scenariosupport.CreatePendingDelivery(ctx, pool, floodTenantID, endpointID); err != nil {
					floodErrMu.Lock()
					floodErr = err
					floodErrMu.Unlock()
				}
			}(endpointID)
		}
	}
	wg.Wait()
	if floodErr != nil {
		return nil, floodErr
	}

	// Quiet tenant publishes concurrently with the flood being drained —
	// measure how long each of its deliveries actually takes to complete.
	quietLatenciesMs := make([]float64, 0, quietEventCount)
	for i := 0; i < quietEventCount; i++ {
		deliveryID, _, err := scenariosupport.CreatePendingDelivery(ctx, pool, quietTenantID, quietEndpointID)
		if err != nil {
			return nil, err
		}
		startedAt := time.Now()
		if err := scenariosupport.WaitUntilDeliveryState(ctx, pool, deliveryID, "succeeded", 15*time.Second); err != nil {
			return nil, err
		}
		quietLatenciesMs = append(quietLatenciesMs, float64(time.Since(startedAt).Microseconds())/1000.0)
		time.Sleep(50 * time.Millisecond)
	}

	sort.Float64s(quietLatenciesMs)
	p50 := scenariosupport.Percentile(quietLatenciesMs, 50)
	p99 := scenariosupport.Percentile(quietLatenciesMs, 99)

	// No hard-coded absolute bound in the spec — the documented mechanism
	// (docs/adr/0004) promises the quiet tenant is never starved, not a
	// specific number. A generous, structural bound: even if every flood
	// endpoint got served once before the quiet tenant, quiet shouldn't
	// wait more than a handful of claim cycles.
	if p99 >= 5_000 {
		return nil, fmt.Errorf("expected quiet tenant's p99 delivery latency under noisy-neighbor load to stay well bounded, got %.1fms", p99)
	}

	var floodCount, floodSucceeded int
	if err := pool.QueryRow(ctx,
		"SELECT count(*), count(*) FILTER (WHERE state = 'succeeded') FROM deliveries WHERE endpoint_id = ANY($1)",
		floodEndpointIDs,
	).Scan(&floodCount, &floodSucceeded); err != nil {
		return nil, err
	}

	return map[string]any{
		"workerCount":         workerCount,
		"floodDeliveryCount":  floodCount,
		"floodSucceededSoFar": floodSucceeded,
		"quietP50Ms":          p50,
		"quietP99Ms":          p99,
		"quietLatenciesMs":    quietLatenciesMs,
	}, nil
}
