// PRD §8 (R-11, R-12): "Fail a partition head; followers report Blocked,
// then drain in order on recovery."
package main

import (
	"context"
	"fmt"
	"net/http"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"webhooks-go/internal/scenariosupport"
)

func main() {
	scenariosupport.RunScenario("chaos", "partition-head-blocked-then-drains", run)
}

// headDeliverySelect + computeBlockedOnDeliveryID mirror
// internal/api/delivery_queries.go's identical logic — duplicated here
// rather than imported, since that logic lives in the api package
// specifically to serve HTTP responses, and this standalone scenario has
// no reason to depend on the api package.
const headDeliverySelect = `
	(SELECT h.id FROM deliveries h
	 WHERE h.endpoint_id = d.endpoint_id AND h.state IN ('pending', 'in_flight')
	 ORDER BY h.seq LIMIT 1)
`

func computeBlockedOnDeliveryID(id, state string, headDeliveryID *string) *string {
	if state != "pending" {
		return nil
	}
	if headDeliveryID != nil && *headDeliveryID == id {
		return nil
	}
	return headDeliveryID
}

func run() (map[string]any, error) {
	ctx := context.Background()
	pool, err := scenariosupport.SetupDatabase(ctx, scenariosupport.ChaosDatabaseName, scenariosupport.ChaosDatabaseURL)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	// The very first request (E1's first attempt) fails; every request
	// after that succeeds — so E1 recovers on its own next backoff retry.
	requestCount := 0
	port, closeReceiver, err := scenariosupport.StartLocalReceiver(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
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

	// Three deliveries on the same endpoint, in publish order — E1 is the
	// head; E2/E3 are the followers that must report Blocked while it's
	// unresolved.
	e1, _, err := scenariosupport.CreatePendingDelivery(ctx, pool, tenantID, endpointID)
	if err != nil {
		return nil, err
	}
	e2, _, err := scenariosupport.CreatePendingDelivery(ctx, pool, tenantID, endpointID)
	if err != nil {
		return nil, err
	}
	e3, _, err := scenariosupport.CreatePendingDelivery(ctx, pool, tenantID, endpointID)
	if err != nil {
		return nil, err
	}

	// A longer backoff after E1's failure — enough for the poll below to
	// reliably observe the "E1 failed, awaiting retry" window and check
	// blocked_on_delivery_id live, instead of racing a retry that could
	// otherwise fire within a single poll interval.
	worker, err := scenariosupport.SpawnWorker("./bin/chaosworker", map[string]string{
		"BACKOFF_BASE_DELAY_MS": "2000",
		"BACKOFF_MAX_DELAY_MS":  "3000",
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = worker.Kill(syscall.SIGKILL) }()

	// E1 has failed its first attempt and is scheduled for retry (still the
	// head — a pending delivery being retried is still unresolved), but
	// hasn't succeeded yet.
	if err := scenariosupport.WaitUntil(ctx, func(ctx context.Context) (bool, error) {
		var state string
		var attemptCount int
		if err := pool.QueryRow(ctx, "SELECT state, attempt_count FROM deliveries WHERE id = $1", e1).Scan(&state, &attemptCount); err != nil {
			return false, err
		}
		return state == "pending" && attemptCount >= 1, nil
	}, 5*time.Second, "E1 fails its first attempt and is scheduled for retry"); err != nil {
		return nil, err
	}

	for _, followerID := range []string{e2, e3} {
		var state string
		var headDeliveryID *string
		if err := pool.QueryRow(ctx,
			fmt.Sprintf("SELECT d.state, %s FROM deliveries d WHERE d.id = $1", headDeliverySelect), followerID,
		).Scan(&state, &headDeliveryID); err != nil {
			return nil, err
		}
		blocked := computeBlockedOnDeliveryID(followerID, state, headDeliveryID)
		if blocked == nil || *blocked != e1 {
			return nil, fmt.Errorf("expected delivery %s to report blocked_on_delivery_id = E1 (%s) while E1 is unresolved, got %v", followerID, e1, blocked)
		}
		if state != "pending" {
			return nil, fmt.Errorf("expected follower %s to still be pending (never dequeued while blocked)", followerID)
		}
	}

	// Recovery: E1 eventually succeeds on retry, then E2 and E3 drain.
	if err := scenariosupport.WaitUntil(ctx, func(ctx context.Context) (bool, error) {
		return allSucceeded(ctx, pool, []string{e1, e2, e3})
	}, 10*time.Second, "E1, E2, and E3 all drain to succeeded after recovery"); err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx,
		`SELECT d.id FROM deliveries d
		 JOIN attempts a ON a.delivery_id = d.id AND a.response_status = 200
		 WHERE d.id = ANY($1)
		 ORDER BY a.sent_at`,
		[]string{e1, e2, e3},
	)
	if err != nil {
		return nil, err
	}
	var drainOrder []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		drainOrder = append(drainOrder, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	expected := []string{e1, e2, e3}
	if len(drainOrder) != 3 || drainOrder[0] != expected[0] || drainOrder[1] != expected[1] || drainOrder[2] != expected[2] {
		return nil, fmt.Errorf("expected the successful attempts to land in publish order E1,E2,E3, got %v", drainOrder)
	}

	return map[string]any{"drainOrder": drainOrder}, nil
}

func allSucceeded(ctx context.Context, pool *pgxpool.Pool, ids []string) (bool, error) {
	rows, err := pool.Query(ctx, "SELECT state FROM deliveries WHERE id = ANY($1)", ids)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			return false, err
		}
		if state != "succeeded" {
			return false, nil
		}
	}
	return true, rows.Err()
}
