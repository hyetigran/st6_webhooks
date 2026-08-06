import type pg from "pg";

// ADR-0005 (docs/adr/): replay mirrors publish's async two-phase shape —
// the durable ack is the replays row alone; this cycle later selects
// matching original deliveries and creates the replayed rows in one
// atomic transaction, then flips the replay to expanded.
//
// Unlike event expansion (ADR-0001), this needs no per-tenant advisory
// lock: that lock exists because two events for the same endpoint must
// expand in publish order, and concurrent unordered expansion would
// break that. Replay has no analogous cross-replay ordering guarantee —
// each replay's own batch gets a correct relative order among itself
// (via deliveries.seq, ADR-0007, inserted in one INSERT...SELECT...ORDER
// BY within one transaction), and two different replays (or a replay and
// live traffic) don't need to be ordered relative to each other. Plain
// FOR UPDATE SKIP LOCKED is enough to stop two workers double-expanding
// the same replay.
//
// The window excludes still-pending/in_flight originals — they'll be
// attempted on their own schedule regardless, so replaying them too is
// pure duplication with no recovery benefit.
export async function runReplayExpansionCycle(pool: pg.Pool): Promise<boolean> {
  const { rows: candidates } = await pool.query<{ id: string }>(
    `SELECT id FROM replays WHERE status = 'pending_expansion' ORDER BY created_at LIMIT 20`,
  );

  for (const { id: replayId } of candidates) {
    const client = await pool.connect();
    try {
      await client.query("BEGIN");

      const { rows: lockedRows } = await client.query<{ id: string; endpoint_id: string; range_start: Date; range_end: Date }>(
        `SELECT id, endpoint_id, range_start, range_end FROM replays
         WHERE id = $1 AND status = 'pending_expansion'
         FOR UPDATE SKIP LOCKED`,
        [replayId],
      );
      const replay = lockedRows[0];
      if (!replay) {
        await client.query("ROLLBACK");
        continue;
      }

      await client.query(
        `INSERT INTO deliveries (event_id, endpoint_id)
         SELECT d.event_id, $1
         FROM deliveries d
         WHERE d.endpoint_id = $1
           AND d.created_at >= $2 AND d.created_at <= $3
           AND d.state NOT IN ('pending', 'in_flight')
         ORDER BY d.seq`,
        [replay.endpoint_id, replay.range_start, replay.range_end],
      );
      await client.query(`UPDATE replays SET status = 'expanded' WHERE id = $1`, [replay.id]);

      await client.query("COMMIT");
      return true;
    } catch (err) {
      await client.query("ROLLBACK");
      throw err;
    } finally {
      client.release();
    }
  }

  return false;
}
