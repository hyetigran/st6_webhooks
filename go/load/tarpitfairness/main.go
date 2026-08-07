// PRD §8 / REVIEW.md F-5 / docs/adr/0004 (R-18): "One tenant saturates the
// worker pool with slow (tarpit) receivers; a quiet tenant's added latency
// stays within roughly one outbound-timeout cycle." The stated bound: once
// any worker frees (a tarpit call completing or timing out), the fairness
// rule routes the *next* claim to whichever tenant has gone longest
// unserved — a tarpit tenant's last_served_at was just bumped when its
// workers were claimed, so a quiet tenant that's never been served (NULL
// sorts first) is served next, not another tarpit endpoint.
package main

import (
	"context"
	"fmt"
	"net/http"
	"syscall"
	"time"

	"webhooks-go/internal/scenariosupport"
)

const (
	workerCount             = 3
	outboundTotalTimeoutMs  = 1_000
	deliveriesPerTarpitHead = 10
)

// ADR-0004's own safety margin for scheduling jitter.
var boundMs = float64(outboundTotalTimeoutMs)*1.5 + 500

func main() {
	scenariosupport.RunScenario("load", "tarpit-fairness", run)
}

func run() (map[string]any, error) {
	ctx := context.Background()
	pool, err := scenariosupport.SetupDatabase(ctx, scenariosupport.LoadDatabaseName, scenariosupport.LoadDatabaseURL)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	// Never responds — every request against this receiver ties up a
	// worker for the full outbound timeout, every time.
	tarpitPort, closeTarpit, err := scenariosupport.StartLocalReceiver(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	if err != nil {
		return nil, err
	}
	defer closeTarpit()

	quietPort, closeQuiet, err := scenariosupport.StartLocalReceiver(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if err != nil {
		return nil, err
	}
	defer closeQuiet()

	tarpitTenantID, _, err := scenariosupport.CreateTenant(ctx, pool, "tarpit-tenant")
	if err != nil {
		return nil, err
	}
	quietTenantID, _, err := scenariosupport.CreateTenant(ctx, pool, "quiet-tenant")
	if err != nil {
		return nil, err
	}

	// One tarpit endpoint per worker — enough to occupy the entire pool at
	// once — each with a deep backlog so a freed worker always finds more
	// tarpit work immediately ready, the worst case for the quiet tenant.
	tarpitEndpointIDs := make([]string, workerCount)
	for i := 0; i < workerCount; i++ {
		endpointID, err := scenariosupport.CreateEndpoint(ctx, pool, tarpitTenantID, []string{"order.created"},
			fmt.Sprintf("http://tarpit.local:%d/hook", tarpitPort), "")
		if err != nil {
			return nil, err
		}
		tarpitEndpointIDs[i] = endpointID
		for j := 0; j < deliveriesPerTarpitHead; j++ {
			if _, _, err := scenariosupport.CreatePendingDelivery(ctx, pool, tarpitTenantID, endpointID); err != nil {
				return nil, err
			}
		}
	}
	quietEndpointID, err := scenariosupport.CreateEndpoint(ctx, pool, quietTenantID, []string{"order.created"},
		fmt.Sprintf("http://quiet.local:%d/hook", quietPort), "")
	if err != nil {
		return nil, err
	}

	workers := make([]*scenariosupport.ManagedProcess, workerCount)
	for i := 0; i < workerCount; i++ {
		w, err := scenariosupport.SpawnChaosWorker("./bin/chaosworker", map[string]string{
			"DATABASE_URL":                 scenariosupport.LoadDatabaseURL,
			"WORKER_IDLE_POLL_INTERVAL_MS": "20",
			"OUTBOUND_TOTAL_TIMEOUT_MS":    fmt.Sprintf("%d", outboundTotalTimeoutMs),
			"OUTBOUND_CONNECT_TIMEOUT_MS":  "1000",
		})
		if err != nil {
			return nil, err
		}
		workers[i] = w
	}
	defer func() {
		for _, w := range workers {
			_ = w.Kill(syscall.SIGKILL)
		}
	}()

	// Let the pool saturate: all workerCount tarpit endpoints busy.
	if err := scenariosupport.WaitUntil(ctx, func(ctx context.Context) (bool, error) {
		var busyCount int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM endpoints WHERE id = ANY($1) AND busy = true", tarpitEndpointIDs,
		).Scan(&busyCount); err != nil {
			return false, err
		}
		return busyCount == workerCount, nil
	}, 5*time.Second, "the worker pool becomes fully saturated with tarpit endpoints"); err != nil {
		return nil, err
	}

	// Now the quiet tenant publishes, into a fully saturated pool.
	startedAt := time.Now()
	quietDeliveryID, _, err := scenariosupport.CreatePendingDelivery(ctx, pool, quietTenantID, quietEndpointID)
	if err != nil {
		return nil, err
	}
	if err := scenariosupport.WaitUntilDeliveryState(ctx, pool, quietDeliveryID, "succeeded", time.Duration(boundMs+5_000)*time.Millisecond); err != nil {
		return nil, fmt.Errorf("the quiet tenant's delivery eventually succeeds: %w", err)
	}
	addedLatencyMs := float64(time.Since(startedAt).Microseconds()) / 1000.0

	if addedLatencyMs > boundMs {
		return nil, fmt.Errorf("expected the quiet tenant's added latency (%.1fms) to stay within roughly one outbound-timeout cycle plus safety margin (%.1fms)", addedLatencyMs, boundMs)
	}

	return map[string]any{
		"workerCount":            workerCount,
		"outboundTotalTimeoutMs": outboundTotalTimeoutMs,
		"boundMs":                boundMs,
		"addedLatencyMs":         addedLatencyMs,
	}, nil
}
