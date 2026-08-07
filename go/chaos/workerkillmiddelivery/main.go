// PRD §8 (R-7, R-17): "Kill a worker mid-delivery; lease expires, work
// resumes, no event lost."
package main

import (
	"context"
	"fmt"
	"net/http"
	"syscall"
	"time"

	"webhooks-go/internal/scenariosupport"
)

func main() {
	scenariosupport.RunScenario("chaos", "worker-kill-mid-delivery", run)
}

func run() (map[string]any, error) {
	ctx := context.Background()
	pool, err := scenariosupport.SetupDatabase(ctx, scenariosupport.ChaosDatabaseName, scenariosupport.ChaosDatabaseURL)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	// Only the *first* request is slow — long enough that worker A is
	// unmistakably still waiting on it when killed, comfortably past both
	// the poll interval used to detect in_flight and A's own (generous)
	// outbound timeout override below. Every request after that responds
	// immediately, so worker B's retry — using the harness's normal, short
	// timeout, not A's — can actually succeed instead of timing out
	// against a receiver that's permanently slower than any worker's
	// patience.
	requestCount := 0
	port, closeReceiver, err := scenariosupport.StartLocalReceiver(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			time.Sleep(4 * time.Second)
		}
		w.WriteHeader(http.StatusOK)
	})
	if err != nil {
		return nil, err
	}
	defer closeReceiver()

	tenantID, _, err := scenariosupport.CreateTenant(ctx, pool, "chaos-tenant")
	if err != nil {
		return nil, err
	}
	endpointID, err := scenariosupport.CreateEndpoint(ctx, pool, tenantID, []string{"order.created"}, fmt.Sprintf("http://chaos-test.local:%d/hook", port), "")
	if err != nil {
		return nil, err
	}
	deliveryID, _, err := scenariosupport.CreatePendingDelivery(ctx, pool, tenantID, endpointID)
	if err != nil {
		return nil, err
	}

	// Worker A gets a generous outbound timeout so it's still genuinely
	// mid-request (not self-timed-out) at the moment it's killed. Worker B
	// uses the harness's small defaults, so its own lease-staleness check
	// only needs to wait out a couple of seconds, not A's 4s+ window.
	workerA, err := scenariosupport.SpawnChaosWorker("./bin/chaosworker", map[string]string{
		"OUTBOUND_TOTAL_TIMEOUT_MS":   "8000",
		"OUTBOUND_CONNECT_TIMEOUT_MS": "8000",
	})
	if err != nil {
		return nil, err
	}

	if err := scenariosupport.WaitUntilDeliveryState(ctx, pool, deliveryID, "in_flight", 5*time.Second); err != nil {
		_ = workerA.Kill(syscall.SIGKILL)
		return nil, fmt.Errorf("worker A claims the delivery: %w", err)
	}

	if err := workerA.Kill(syscall.SIGKILL); err != nil {
		return nil, err
	}

	workerB, err := scenariosupport.SpawnChaosWorker("./bin/chaosworker", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = workerB.Kill(syscall.SIGKILL) }()

	if err := scenariosupport.WaitUntilDeliveryState(ctx, pool, deliveryID, "succeeded", 15*time.Second); err != nil {
		return nil, fmt.Errorf("worker B reclaims after lease expiry and completes the delivery: %w", err)
	}

	rows, err := pool.Query(ctx, "SELECT error_class, response_status FROM attempts WHERE delivery_id = $1 ORDER BY attempt_number", deliveryID)
	if err != nil {
		return nil, err
	}
	type attemptRow struct {
		ErrorClass     *string
		ResponseStatus *int
	}
	var attempts []attemptRow
	for rows.Next() {
		var a attemptRow
		if err := rows.Scan(&a.ErrorClass, &a.ResponseStatus); err != nil {
			rows.Close()
			return nil, err
		}
		attempts = append(attempts, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(attempts) < 2 {
		return nil, fmt.Errorf("expected at least 2 attempts (killed A's orphan + B's successful one), got %d", len(attempts))
	}
	if attempts[0].ErrorClass == nil || *attempts[0].ErrorClass != "worker_lease_expired" {
		return nil, fmt.Errorf("expected the orphaned first attempt to be closed with worker_lease_expired, got %v", attempts[0].ErrorClass)
	}
	last := attempts[len(attempts)-1]
	if last.ResponseStatus == nil || *last.ResponseStatus != 200 {
		return nil, fmt.Errorf("expected the final attempt to have succeeded with a 200")
	}

	var finalState string
	if err := pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE id = $1", deliveryID).Scan(&finalState); err != nil {
		return nil, err
	}
	if finalState != "succeeded" {
		return nil, fmt.Errorf("delivery must end in exactly one terminal state: succeeded, got %s", finalState)
	}

	return map[string]any{"attemptCount": len(attempts), "finalState": finalState}, nil
}
