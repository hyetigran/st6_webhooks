// Package worker holds the shared worker pool's poll-loop cycles.
package worker

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunExpansionCycle turns one published event into per-endpoint delivery
// rows. docs/adr/0001: expansion is serialized per tenant via an advisory
// transaction lock, not left fully parallel — naive parallel claiming lets
// two events for the same endpoint expand out of publish order. The lock
// auto-releases on commit or crash, so this needs no lease/reaper (matching
// the reasoning for why expansion never needed one: no external I/O to get
// stuck on). Cross-tenant expansion stays fully parallel; only within-tenant
// expansion serializes — exactly the scope the per-endpoint ordering
// guarantee needs, since every endpoint belongs to one tenant.
//
// Returns true if an event was expanded this cycle, false if there was
// nothing to do (every candidate tenant was either empty or already locked
// by another worker).
func RunExpansionCycle(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	rows, err := pool.Query(ctx,
		`SELECT tenant_id FROM events WHERE status = 'pending_expansion'
		 GROUP BY tenant_id ORDER BY MIN(seq) LIMIT 20`,
	)
	if err != nil {
		return false, err
	}
	var candidates []string
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			rows.Close()
			return false, err
		}
		candidates = append(candidates, tenantID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	for _, tenantID := range candidates {
		did, err := expandOneTenant(ctx, pool, tenantID)
		if err != nil {
			return false, err
		}
		if did {
			return true, nil
		}
	}

	return false, nil
}

func expandOneTenant(ctx context.Context, pool *pgxpool.Pool, tenantID string) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	// Rollback after a successful Commit is a documented no-op in pgx, so
	// this unconditional defer is safe on every exit path.
	defer tx.Rollback(ctx)

	// hashtext() collapses the UUID to a 32-bit int; collisions just mean
	// two different tenants can't expand in parallel with each other for
	// one cycle — a throughput cost, not a correctness bug.
	var locked bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtext($1)::bigint)", tenantID).Scan(&locked); err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}

	var eventID, eventType string
	err = tx.QueryRow(ctx,
		`SELECT id, type FROM events
		 WHERE tenant_id = $1 AND status = 'pending_expansion'
		 ORDER BY seq LIMIT 1`,
		tenantID,
	).Scan(&eventID, &eventType)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // nothing pending_expansion for this tenant right now
	}
	if err != nil {
		return false, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO deliveries (event_id, endpoint_id)
		 SELECT $1, id FROM endpoints WHERE tenant_id = $2 AND $3 = ANY(event_types)`,
		eventID, tenantID, eventType,
	); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, "UPDATE events SET status = 'expanded' WHERE id = $1", eventID); err != nil {
		return false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
