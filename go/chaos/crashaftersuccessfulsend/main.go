// PRD §8 (R-15): a worker whose outbound send reached the receiver and
// succeeded, but which crashes before writing that outcome back, must not
// cause the delivery to be lost or double-counted as a distinct failure —
// the second worker's retry resolves it, and the receiver sees exactly two
// attempts for the same logical delivery (both bearing the same
// Webhook-Event-Id), not evidence of duplicate/lost work.
//
// This is workerstallfencing's sibling: that scenario resumes the stalled
// worker (SIGCONT) to prove its late write is fenced out. This one never
// resumes it at all (SIGKILL instead) — the simpler, more common case of a
// crash that just never comes back — proving recovery doesn't depend on the
// crashed worker ever writing anything.
package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"syscall"
	"time"

	"webhooks-go/internal/scenariosupport"
)

func main() {
	scenariosupport.RunScenario("chaos", "crash-after-successful-send", run)
}

func run() (map[string]any, error) {
	ctx := context.Background()
	pool, err := scenariosupport.SetupDatabase(ctx, scenariosupport.ChaosDatabaseName, scenariosupport.ChaosDatabaseURL)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	var mu sync.Mutex
	var eventIDsSeen []string
	requestCount := 0
	// The first request gets a short delay before responding 200 — just
	// enough for the poll loop below to observe in_flight and SIGSTOP A
	// after the receiver has already sent its response but before A can
	// process it, mirroring workerstallfencing's timing. A is then killed
	// outright (never resumed), so its outbound request having "succeeded"
	// from the receiver's point of view is never reflected in the DB.
	port, closeReceiver, err := scenariosupport.StartLocalReceiver(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		n := requestCount
		mu.Unlock()
		if n == 1 {
			time.Sleep(500 * time.Millisecond)
		}
		mu.Lock()
		eventIDsSeen = append(eventIDsSeen, r.Header.Get("Webhook-Event-Id"))
		mu.Unlock()
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
	deliveryID, eventID, err := scenariosupport.CreatePendingDelivery(ctx, pool, tenantID, endpointID)
	if err != nil {
		return nil, err
	}

	workerA, err := scenariosupport.SpawnWorker("./bin/chaosworker", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = workerA.Kill(syscall.SIGKILL) }()

	if err := scenariosupport.WaitUntilDeliveryState(ctx, pool, deliveryID, "in_flight", 5*time.Second); err != nil {
		return nil, fmt.Errorf("worker A claims the delivery: %w", err)
	}

	// Freeze A once its request has landed at the receiver (the receiver's
	// 500ms sleep guarantees this poll interval catches it mid-flight, well
	// before A could have processed the response and written back).
	if err := workerA.Kill(syscall.SIGSTOP); err != nil {
		return nil, err
	}
	// SIGKILL cannot be delivered to a stopped process's normal signal
	// handling — SIGKILL always takes effect immediately regardless of stop
	// state, terminating A for good without ever letting it resume and
	// write back the response it already received.
	if err := workerA.Kill(syscall.SIGKILL); err != nil {
		return nil, err
	}

	workerB, err := scenariosupport.SpawnWorker("./bin/chaosworker", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = workerB.Kill(syscall.SIGKILL) }()

	if err := scenariosupport.WaitUntilDeliveryState(ctx, pool, deliveryID, "succeeded", 10*time.Second); err != nil {
		return nil, fmt.Errorf("worker B reclaims after A's crash and completes the delivery: %w", err)
	}

	var finalState string
	if err := pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE id = $1", deliveryID).Scan(&finalState); err != nil {
		return nil, err
	}
	if finalState != "succeeded" {
		return nil, fmt.Errorf("delivery must end in exactly one terminal state: succeeded, got %s", finalState)
	}

	mu.Lock()
	seen := append([]string(nil), eventIDsSeen...)
	mu.Unlock()
	if len(seen) != 2 {
		return nil, fmt.Errorf("expected the receiver to be hit exactly twice (A's crashed send + B's successful retry), got %d", len(seen))
	}
	if seen[0] != eventID || seen[1] != eventID {
		return nil, fmt.Errorf("expected both hits to carry the same Webhook-Event-Id %s, got %v", eventID, seen)
	}

	return map[string]any{"finalState": finalState, "receiverHits": len(seen)}, nil
}
