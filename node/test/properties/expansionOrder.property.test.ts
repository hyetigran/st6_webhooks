import { describe, it, expect } from "vitest";
import { pool } from "../../src/db/pool.js";
import { runExpansionCycle } from "../../src/worker/expansion.js";
import { createTenant, createEndpoint } from "../fixtures.js";
import { mulberry32, getTestSeed, randomInt } from "./prng.js";

async function publishEvent(tenantId: string, idempotencyKey: string): Promise<string> {
  const { rows } = await pool.query<{ id: string }>(
    `INSERT INTO events (tenant_id, idempotency_key, type, payload) VALUES ($1, $2, 'order.created', '{}') RETURNING id`,
    [tenantId, idempotencyKey],
  );
  return rows[0]!.id;
}

// Drains every pending_expansion event by racing several concurrent workers
// against the real advisory-lock claim — the actual concurrency-safety
// property under test, not simulated.
async function drainExpansion(concurrency: number): Promise<void> {
  for (;;) {
    const results = await Promise.all(Array.from({ length: concurrency }, () => runExpansionCycle(pool)));
    if (results.every((didWork) => !didWork)) return;
  }
}

// REVIEW.md F-1 / docs/adr/0001: per-endpoint delivery order must equal
// events.seq order, even when expansion runs fully concurrently — the
// per-tenant advisory lock exists specifically to make this true under
// concurrent workers, not just under the sequential expansion the earlier
// example-based tests (expansion.test.ts) exercise.
describe("expansion order property: per-endpoint delivery order equals events.seq order", () => {
  it("holds for a randomized number of events published to one endpoint, expanded by racing concurrent workers", async () => {
    const seed = getTestSeed("expansion-order");
    const rng = mulberry32(seed);
    const eventCount = randomInt(rng, 5, 15);
    const concurrency = randomInt(rng, 2, 5);

    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);

    const publishedEventIdsInOrder: string[] = [];
    for (let i = 0; i < eventCount; i++) {
      publishedEventIdsInOrder.push(await publishEvent(tenantId, `expansion-order-${seed}-${i}`));
    }

    await drainExpansion(concurrency);

    const { rows: deliveries } = await pool.query<{ event_id: string }>(
      "SELECT event_id FROM deliveries WHERE endpoint_id = $1 ORDER BY seq",
      [endpoint.id],
    );

    expect(deliveries.map((d) => d.event_id)).toEqual(publishedEventIdsInOrder);
  });
});
