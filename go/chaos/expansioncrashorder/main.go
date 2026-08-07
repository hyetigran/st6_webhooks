// PRD §8 (R-8, R-11) / REVIEW.md F-1 / docs/adr/0001: a crashed expansion
// worker must never leave the per-tenant advisory lock stuck held, and
// strict publish-order expansion must survive the crash — the next
// expansion cycle picks up exactly where the dead one left off (it never
// got past acquiring the lock, so nothing was expanded yet), in order.
//
// Uses the standalone expansionholder binary (cmd/expansionholder) as a
// stand-in for "a real expansion worker died mid-transaction, having done
// nothing beyond acquiring the lock": it opens a transaction, acquires the
// tenant's advisory lock, and waits indefinitely without committing —
// exactly what an expansion worker crashing right after the lock acquire
// looks like from Postgres's point of view. A crash here means the
// transaction (and the session-scoped advisory lock it holds) simply never
// resolves until Postgres notices the connection died — which is what
// SIGKILLing the process forces.
package main

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/google/uuid"

	"webhooks-go/internal/scenariosupport"
	"webhooks-go/internal/worker"
)

func main() {
	scenariosupport.RunScenario("chaos", "expansion-crash-order", run)
}

func run() (map[string]any, error) {
	ctx := context.Background()
	pool, err := scenariosupport.SetupDatabase(ctx, scenariosupport.ChaosDatabaseName, scenariosupport.ChaosDatabaseURL)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	tenantID, _, err := scenariosupport.CreateTenant(ctx, pool, "chaos-tenant")
	if err != nil {
		return nil, err
	}
	endpointID, err := scenariosupport.CreateEndpoint(ctx, pool, tenantID, []string{"order.created"}, "https://example.com/hook", "")
	if err != nil {
		return nil, err
	}

	const eventCount = 8
	publishedEventIDs := make([]string, eventCount)
	for i := 0; i < eventCount; i++ {
		if err := pool.QueryRow(ctx,
			`INSERT INTO events (tenant_id, idempotency_key, type, payload, status)
			 VALUES ($1, $2, 'order.created', '{"hello":"chaos"}', 'pending_expansion')
			 RETURNING id`,
			tenantID, "expansion-order-fixture-"+uuid.NewString(),
		).Scan(&publishedEventIDs[i]); err != nil {
			return nil, err
		}
	}

	holder, err := scenariosupport.StartProcess("./bin/expansionholder", []string{tenantID},
		[]string{"DATABASE_URL=" + scenariosupport.ChaosDatabaseURL})
	if err != nil {
		return nil, err
	}
	holderKilled := false
	defer func() {
		if !holderKilled {
			_ = holder.Kill(syscall.SIGKILL)
		}
	}()

	// Confirms the lock is genuinely contested (not just that the holder
	// process exists): repeatedly attempts to acquire the same advisory
	// lock from a separate, immediately-rolled-back transaction — as long
	// as the holder is alive and holding it, this must keep failing.
	if err := scenariosupport.WaitUntil(ctx, func(ctx context.Context) (bool, error) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return false, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var locked bool
		if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtext($1)::bigint)", tenantID).Scan(&locked); err != nil {
			return false, err
		}
		return !locked, nil
	}, 5*time.Second, "expansionholder acquires and holds the tenant's advisory lock"); err != nil {
		return nil, err
	}

	// A real expansion cycle attempted right now must find the lock busy
	// and do no work — proving the crash scenario we're about to create
	// (killing the holder) is the actual mechanism recovering the lock,
	// not some pre-existing gap.
	didWorkWhileHeld, err := worker.RunExpansionCycle(ctx, pool)
	if err != nil {
		return nil, err
	}
	if didWorkWhileHeld {
		return nil, fmt.Errorf("expected expansion to find the advisory lock contended and do no work while the holder is alive")
	}

	if err := holder.Kill(syscall.SIGKILL); err != nil {
		return nil, err
	}
	holderKilled = true

	// The crash releases the lock (session-scoped, tied to the dead
	// connection) without ever having expanded anything — the next real
	// expansion cycles must now drain every event, strictly in publish
	// order, exactly as if the crash never happened.
	for {
		didWork, err := worker.RunExpansionCycle(ctx, pool)
		if err != nil {
			return nil, err
		}
		if !didWork {
			break
		}
	}

	rows, err := pool.Query(ctx, "SELECT event_id FROM deliveries WHERE endpoint_id = $1 ORDER BY seq", endpointID)
	if err != nil {
		return nil, err
	}
	var deliveredEventIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		deliveredEventIDs = append(deliveredEventIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(deliveredEventIDs) != eventCount {
		return nil, fmt.Errorf("expected all %d events to expand after the crashed holder's lock was released, got %d", eventCount, len(deliveredEventIDs))
	}
	for i := range publishedEventIDs {
		if deliveredEventIDs[i] != publishedEventIDs[i] {
			return nil, fmt.Errorf("expected strict publish order to survive the crash, got %v, want %v", deliveredEventIDs, publishedEventIDs)
		}
	}

	return map[string]any{"eventCount": eventCount}, nil
}
