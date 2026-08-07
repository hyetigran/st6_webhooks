package worker

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunReplayExpansionCycle turns one pending replay into fresh delivery rows.
// docs/adr/0005: replay mirrors publish's async two-phase shape — the
// durable ack is the replays row alone; this cycle later selects matching
// original deliveries and creates the replayed rows in one atomic
// transaction, then flips the replay to expanded.
//
// Unlike event expansion (docs/adr/0001), this needs no per-tenant advisory
// lock: that lock exists because two events for the same endpoint must
// expand in publish order, and concurrent unordered expansion would break
// that. Replay has no analogous cross-replay ordering guarantee — each
// replay's own batch gets a correct relative order among itself (via
// deliveries.seq, docs/adr/0007, inserted in one INSERT...SELECT...ORDER BY
// within one transaction), and two different replays (or a replay and live
// traffic) don't need to be ordered relative to each other. Plain
// FOR UPDATE SKIP LOCKED is enough to stop two workers double-expanding the
// same replay.
//
// The window excludes still-pending/in_flight originals — they'll be
// attempted on their own schedule regardless, so replaying them too is pure
// duplication with no recovery benefit.
func RunReplayExpansionCycle(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	rows, err := pool.Query(ctx, "SELECT id FROM replays WHERE status = 'pending_expansion' ORDER BY created_at LIMIT 20")
	if err != nil {
		return false, err
	}
	var candidates []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		candidates = append(candidates, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	for _, replayID := range candidates {
		did, err := expandOneReplay(ctx, pool, replayID)
		if err != nil {
			return false, err
		}
		if did {
			return true, nil
		}
	}
	return false, nil
}

func expandOneReplay(ctx context.Context, pool *pgxpool.Pool, replayID string) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var endpointID string
	var rangeStart, rangeEnd time.Time
	err = tx.QueryRow(ctx,
		`SELECT endpoint_id, range_start, range_end FROM replays
		 WHERE id = $1 AND status = 'pending_expansion'
		 FOR UPDATE SKIP LOCKED`,
		replayID,
	).Scan(&endpointID, &rangeStart, &rangeEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // another worker already claimed or expanded it
	}
	if err != nil {
		return false, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO deliveries (event_id, endpoint_id)
		 SELECT d.event_id, $1
		 FROM deliveries d
		 WHERE d.endpoint_id = $1
		   AND d.created_at >= $2 AND d.created_at <= $3
		   AND d.state NOT IN ('pending', 'in_flight')
		 ORDER BY d.seq`,
		endpointID, rangeStart, rangeEnd,
	); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, "UPDATE replays SET status = 'expanded' WHERE id = $1", replayID); err != nil {
		return false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
