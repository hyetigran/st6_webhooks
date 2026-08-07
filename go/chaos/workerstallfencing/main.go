// REVIEW.md F-2 / docs/adr/0002 / PRD §8 (R-11, R-15, R-17): "SIGSTOP a
// worker mid-request past lease expiry, let another reclaim and complete,
// SIGCONT the first; its write is dropped, exactly one terminal state, no
// interval with two in-flight attempts."
//
// A dead worker (kill -9) never writes back — the simple kill-mid-delivery
// scenario covers that. This one is the harder case ADR-0002 exists for: a
// *stalled* (SIGSTOPped, not dead) worker whose outbound request may have
// already reached the receiver, but who hasn't written the outcome back
// yet. Because SIGSTOP freezes the process entirely (no timers, no
// scheduler), the receiver's response just sits in the kernel socket
// buffer, unread, for as long as the stop lasts — exactly reproducing "the
// send reached the receiver before the lease was reclaimed."
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
	scenariosupport.RunScenario("chaos", "worker-stall-fencing", run)
}

func run() (map[string]any, error) {
	ctx := context.Background()
	pool, err := scenariosupport.SetupDatabase(ctx, scenariosupport.ChaosDatabaseName, scenariosupport.ChaosDatabaseURL)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	// The first request gets a small delay — just enough for the poll loop
	// below to observe in_flight and SIGSTOP A before the request would
	// otherwise complete on its own. Once frozen, A stays frozen regardless
	// of how much of that delay remains. Later requests (B's, after
	// reclaim) respond immediately.
	requestCount := 0
	port, closeReceiver, err := scenariosupport.StartLocalReceiver(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			time.Sleep(500 * time.Millisecond)
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

	// Sequential, not concurrent, spawning — deterministically makes A the
	// claimer, rather than racing two fresh workers for who gets there
	// first.
	workerA, err := scenariosupport.SpawnWorker("./bin/chaosworker", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = workerA.Kill(syscall.SIGKILL) }()

	if err := scenariosupport.WaitUntilDeliveryState(ctx, pool, deliveryID, "in_flight", 5*time.Second); err != nil {
		return nil, fmt.Errorf("worker A claims the delivery: %w", err)
	}

	var staleLeaseID string
	if err := pool.QueryRow(ctx, "SELECT lease_id FROM endpoints WHERE id = $1", endpointID).Scan(&staleLeaseID); err != nil {
		return nil, err
	}
	// Freeze A before it can process the (already-sent) response.
	if err := workerA.Kill(syscall.SIGSTOP); err != nil {
		return nil, err
	}

	workerB, err := scenariosupport.SpawnWorker("./bin/chaosworker", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = workerB.Kill(syscall.SIGKILL) }()

	if err := scenariosupport.WaitUntilDeliveryState(ctx, pool, deliveryID, "succeeded", 10*time.Second); err != nil {
		return nil, fmt.Errorf("worker B reclaims the stale lease and completes the delivery: %w", err)
	}

	var firstErrorClass *string
	if err := pool.QueryRow(ctx,
		"SELECT error_class FROM attempts WHERE delivery_id = $1 ORDER BY attempt_number LIMIT 1", deliveryID,
	).Scan(&firstErrorClass); err != nil {
		return nil, err
	}
	if firstErrorClass == nil || *firstErrorClass != "worker_lease_expired" {
		return nil, fmt.Errorf("expected A's orphaned attempt to be closed with worker_lease_expired before resuming A, got %v", firstErrorClass)
	}

	// Now let A wake up. Its outbound request already completed (the
	// receiver responded immediately, back when A was still running) — A
	// will finally process that response and attempt to write back. Its
	// lease_id no longer matches the endpoint's current one (B's), so
	// docs/adr/0002 says this write must be silently dropped.
	if err := workerA.Kill(syscall.SIGCONT); err != nil {
		return nil, err
	}
	// Give A's resumed write-back a real window to run (and, if the
	// fencing were broken, to corrupt state) before asserting anything.
	time.Sleep(2 * time.Second)

	var finalLeaseID *string
	var busy bool
	if err := pool.QueryRow(ctx, "SELECT lease_id, busy FROM endpoints WHERE id = $1", endpointID).Scan(&finalLeaseID, &busy); err != nil {
		return nil, err
	}
	if finalLeaseID != nil && *finalLeaseID == staleLeaseID {
		return nil, fmt.Errorf("the endpoint's lease_id must not have been reset back to A's stale value by A's late write")
	}
	if busy {
		return nil, fmt.Errorf("the endpoint must not be left busy by A's dropped write")
	}

	rows, err := pool.Query(ctx, "SELECT response_status FROM attempts WHERE delivery_id = $1 ORDER BY attempt_number", deliveryID)
	if err != nil {
		return nil, err
	}
	var attemptCount, withResponses int
	for rows.Next() {
		var status *int
		if err := rows.Scan(&status); err != nil {
			rows.Close()
			return nil, err
		}
		attemptCount++
		if status != nil {
			withResponses++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Exactly one attempt ever recorded a real response — A's write was
	// fenced out entirely, not merely overwritten a second time.
	if withResponses != 1 {
		return nil, fmt.Errorf("expected exactly one attempt with a recorded response (B's), got %d", withResponses)
	}

	var finalState string
	if err := pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE id = $1", deliveryID).Scan(&finalState); err != nil {
		return nil, err
	}
	if finalState != "succeeded" {
		return nil, fmt.Errorf("delivery must remain in exactly one terminal state: succeeded, got %s", finalState)
	}

	return map[string]any{"attemptCount": attemptCount, "finalState": finalState}, nil
}
