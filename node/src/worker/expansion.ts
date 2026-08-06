import type pg from "pg";

// ADR-0001 (docs/adr/): expansion is serialized per tenant via an advisory
// transaction lock, not left fully parallel — naive parallel claiming lets
// two events for the same endpoint expand out of publish order. The lock
// auto-releases on commit or crash, so this needs no lease/reaper, matching
// ADR-004's original reasoning (expansion has no external I/O to get stuck
// on). Cross-tenant expansion stays fully parallel; only within-tenant
// expansion serializes — which is exactly the scope the per-endpoint
// ordering guarantee (ADR-002) needs, since every endpoint belongs to one
// tenant.
//
// Returns true if an event was expanded this cycle, false if there was
// nothing to do (every candidate tenant was either empty or already locked
// by another worker).
export async function runExpansionCycle(pool: pg.Pool): Promise<boolean> {
  const { rows: candidates } = await pool.query<{ tenant_id: string }>(
    `SELECT tenant_id FROM events WHERE status = 'pending_expansion'
     GROUP BY tenant_id ORDER BY MIN(seq) LIMIT 20`,
  );

  for (const { tenant_id: tenantId } of candidates) {
    const client = await pool.connect();
    try {
      await client.query("BEGIN");

      const { rows: lockRows } = await client.query<{ locked: boolean }>(
        // hashtext() collapses the UUID to a 32-bit int; collisions just
        // mean two different tenants can't expand in parallel with each
        // other for one cycle — a throughput cost, not a correctness bug.
        "SELECT pg_try_advisory_xact_lock(hashtext($1)::bigint) AS locked",
        [tenantId],
      );
      if (!lockRows[0]!.locked) {
        await client.query("ROLLBACK");
        continue;
      }

      const { rows: eventRows } = await client.query<{ id: string; type: string }>(
        `SELECT id, type FROM events
         WHERE tenant_id = $1 AND status = 'pending_expansion'
         ORDER BY seq LIMIT 1`,
        [tenantId],
      );
      const event = eventRows[0];
      if (!event) {
        await client.query("COMMIT");
        continue;
      }

      await client.query(
        `INSERT INTO deliveries (event_id, endpoint_id)
         SELECT $1, id FROM endpoints WHERE tenant_id = $2 AND $3 = ANY(event_types)`,
        [event.id, tenantId, event.type],
      );
      await client.query(`UPDATE events SET status = 'expanded' WHERE id = $1`, [event.id]);

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
