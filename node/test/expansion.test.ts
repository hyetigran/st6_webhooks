import { describe, it, expect } from "vitest";
import { pool } from "../src/db/pool.js";
import { runExpansionCycle } from "../src/worker/expansion.js";
import { createTenant, createEndpoint } from "./fixtures.js";

async function publishEvent(tenantId: string, type: string, idempotencyKey: string): Promise<string> {
  const { rows } = await pool.query<{ id: string }>(
    `INSERT INTO events (tenant_id, idempotency_key, type, payload) VALUES ($1, $2, $3, '{}') RETURNING id`,
    [tenantId, idempotencyKey, type],
  );
  return rows[0]!.id;
}

describe("runExpansionCycle", () => {
  it("creates one delivery per subscribed endpoint and flips the event to expanded", async () => {
    const { id: tenantId } = await createTenant();
    const endpointA = await createEndpoint(tenantId, ["order.created"]);
    const endpointB = await createEndpoint(tenantId, ["order.created"]);
    const eventId = await publishEvent(tenantId, "order.created", "key-1");

    await runExpansionCycle(pool);

    const { rows: eventRows } = await pool.query<{ status: string }>(
      "SELECT status FROM events WHERE id = $1",
      [eventId],
    );
    expect(eventRows[0]!.status).toBe("expanded");

    const { rows: deliveryRows } = await pool.query<{ endpoint_id: string }>(
      "SELECT endpoint_id FROM deliveries WHERE event_id = $1 ORDER BY endpoint_id",
      [eventId],
    );
    const deliveredEndpointIds = deliveryRows.map((r) => r.endpoint_id).sort();
    expect(deliveredEndpointIds).toEqual([endpointA.id, endpointB.id].sort());
  });

  it("flips an event to expanded even when no endpoint is subscribed to its type", async () => {
    const { id: tenantId } = await createTenant();
    await createEndpoint(tenantId, ["payment.failed"]); // not subscribed to order.created
    const eventId = await publishEvent(tenantId, "order.created", "key-2");

    await runExpansionCycle(pool);

    const { rows: eventRows } = await pool.query<{ status: string }>(
      "SELECT status FROM events WHERE id = $1",
      [eventId],
    );
    expect(eventRows[0]!.status).toBe("expanded");

    const { rows: deliveryRows } = await pool.query("SELECT 1 FROM deliveries WHERE event_id = $1", [eventId]);
    expect(deliveryRows).toHaveLength(0);
  });

  it("expands events in publish (seq) order across separate cycle calls", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const firstEventId = await publishEvent(tenantId, "order.created", "seq-key-1");
    const secondEventId = await publishEvent(tenantId, "order.created", "seq-key-2");

    // Two separate cycles — each call claims and expands exactly one event.
    await runExpansionCycle(pool);
    await runExpansionCycle(pool);

    const { rows: deliveryRows } = await pool.query<{ event_id: string }>(
      "SELECT event_id FROM deliveries WHERE endpoint_id = $1 ORDER BY created_at, id",
      [endpoint.id],
    );
    expect(deliveryRows.map((r) => r.event_id)).toEqual([firstEventId, secondEventId]);
  });

  it("queues deliveries for paused and halted endpoints too, not just active ones", async () => {
    const { id: tenantId } = await createTenant();
    const pausedEndpoint = await createEndpoint(tenantId, ["order.created"], { status: "paused" });
    const haltedEndpoint = await createEndpoint(tenantId, ["order.created"], { status: "halted" });
    const eventId = await publishEvent(tenantId, "order.created", "key-status-test");

    await runExpansionCycle(pool);

    const { rows: deliveryRows } = await pool.query<{ endpoint_id: string }>(
      "SELECT endpoint_id FROM deliveries WHERE event_id = $1",
      [eventId],
    );
    const deliveredEndpointIds = deliveryRows.map((r) => r.endpoint_id).sort();
    expect(deliveredEndpointIds).toEqual([pausedEndpoint.id, haltedEndpoint.id].sort());
  });
});
