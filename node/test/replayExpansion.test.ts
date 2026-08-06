import { describe, it, expect } from "vitest";
import { pool } from "../src/db/pool.js";
import { runReplayExpansionCycle } from "../src/worker/replayExpansion.js";
import { createTenant, createEndpoint, createDelivery } from "./fixtures.js";

async function createReplay(
  endpointId: string,
  rangeStart: Date,
  rangeEnd: Date,
  idempotencyKey = `replay-fixture-${crypto.randomUUID()}`,
): Promise<string> {
  const { rows } = await pool.query<{ id: string }>(
    `INSERT INTO replays (endpoint_id, idempotency_key, range_start, range_end) VALUES ($1, $2, $3, $4) RETURNING id`,
    [endpointId, idempotencyKey, rangeStart, rangeEnd],
  );
  return rows[0]!.id;
}

const windowStart = new Date(Date.now() - 60_000);
const windowEnd = new Date(Date.now() + 60_000);
const beforeWindow = new Date(Date.now() - 120_000);

describe("runReplayExpansionCycle", () => {
  it("creates a fresh delivery row per terminal original in the window and flips the replay to expanded", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const original = await createDelivery(tenantId, endpoint.id, { state: "succeeded", createdAt: windowStart });
    const replayId = await createReplay(endpoint.id, windowStart, windowEnd);

    const didWork = await runReplayExpansionCycle(pool);

    expect(didWork).toBe(true);

    const { rows: replayRows } = await pool.query<{ status: string }>("SELECT status FROM replays WHERE id = $1", [replayId]);
    expect(replayRows[0]!.status).toBe("expanded");

    const { rows: deliveryRows } = await pool.query<{ id: string; event_id: string; state: string }>(
      "SELECT id, event_id, state FROM deliveries WHERE endpoint_id = $1 AND id != $2",
      [endpoint.id, original.id],
    );
    expect(deliveryRows).toHaveLength(1);
    expect(deliveryRows[0]!.event_id).toBe(original.eventId);
    expect(deliveryRows[0]!.state).toBe("pending");
    expect(deliveryRows[0]!.id).not.toBe(original.id); // fresh delivery_id, per R-19/R-20
  });

  it("excludes originals outside the replay window", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    await createDelivery(tenantId, endpoint.id, { state: "succeeded", createdAt: beforeWindow });
    await createReplay(endpoint.id, windowStart, windowEnd);

    await runReplayExpansionCycle(pool);

    const { rows } = await pool.query<{ count: string }>(
      "SELECT count(*) FROM deliveries WHERE endpoint_id = $1 AND state != 'succeeded'",
      [endpoint.id],
    );
    expect(Number(rows[0]!.count)).toBe(0);
  });

  it("excludes still-pending and in_flight originals, since they'll be attempted on their own schedule regardless", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    await createDelivery(tenantId, endpoint.id, { state: "pending", createdAt: windowStart });
    await createDelivery(tenantId, endpoint.id, { state: "in_flight", createdAt: windowStart });
    await createReplay(endpoint.id, windowStart, windowEnd);

    await runReplayExpansionCycle(pool);

    const { rows } = await pool.query<{ count: string }>(
      "SELECT count(*) FROM deliveries WHERE endpoint_id = $1",
      [endpoint.id],
    );
    expect(Number(rows[0]!.count)).toBe(2); // only the two originals, no replayed rows added
  });

  it("inserts replayed deliveries in the original chronological order (by seq)", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const first = await createDelivery(tenantId, endpoint.id, { state: "succeeded", createdAt: windowStart });
    const second = await createDelivery(tenantId, endpoint.id, { state: "failed", createdAt: windowStart });
    await createReplay(endpoint.id, windowStart, windowEnd);

    await runReplayExpansionCycle(pool);

    const { rows } = await pool.query<{ event_id: string }>(
      "SELECT event_id FROM deliveries WHERE endpoint_id = $1 AND state = 'pending' ORDER BY seq",
      [endpoint.id],
    );
    expect(rows.map((r) => r.event_id)).toEqual([first.eventId, second.eventId]);
  });

  it("returns false when there is nothing pending to expand", async () => {
    const didWork = await runReplayExpansionCycle(pool);
    expect(didWork).toBe(false);
  });
});
