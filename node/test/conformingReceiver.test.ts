import { describe, it, expect, afterEach } from "vitest";
import request from "supertest";
import { createApp } from "../src/app.js";
import { pool } from "../src/db/pool.js";
import { runDeliveryCycle } from "../src/worker/delivery.js";
import { runReplayExpansionCycle } from "../src/worker/replayExpansion.js";
import { createTenant, createDelivery } from "./fixtures.js";
import { createTestServerHarness } from "./testServer.js";
import { createConformingReceiver } from "./conformingReceiver.js";

const testServer = createTestServerHarness();
afterEach(() => testServer.close());

const trustLoopback = async (): Promise<{ allowed: true; ip: string }> => ({ allowed: true, ip: "127.0.0.1" });

async function deliverOnce(): Promise<boolean> {
  return runDeliveryCycle(pool, { resolveAndPin: trustLoopback });
}

// REVIEW.md F-13 / PRD §6: a conforming receiver dedupes on *successfully
// processed* event_id — not on event_id merely seen. This is the property
// that makes replaying a previously-*failed* event actually work; a
// receiver that (incorrectly) marked "seen" at attempt time would silently
// no-op that replay, defeating the reason to replay it in the first place.
describe("conforming receiver (F-13)", () => {
  it("a replay of an event that already succeeded is idempotently no-op'd, not reprocessed", async () => {
    const receiver = createConformingReceiver((_eventId, attemptNumber) => attemptNumber >= 2); // fails once, then succeeds
    const port = await testServer.listen(receiver.handler);

    const { apiKey, id: tenantId } = await createTenant();
    const app = createApp();
    const register = await request(app)
      .post("/endpoints")
      .set("Authorization", `Bearer ${apiKey}`)
      .send({ url: `http://example.com:${port}/hook`, event_types: ["order.created"] });
    const endpointId: string = register.body.id;

    const { eventId } = await createDelivery(tenantId, endpointId);

    await deliverOnce(); // attempt 1: receiver fails it
    await new Promise((r) => setTimeout(r, 150)); // past the small injected backoff delay
    await deliverOnce(); // attempt 2: receiver succeeds, marks processed

    expect(receiver.processedEventIds.has(eventId)).toBe(true);
    expect(receiver.attemptsByEventId.get(eventId)).toBe(2);

    // Replay it — a fresh delivery row, same event_id.
    const replay = await request(app)
      .post(`/endpoints/${endpointId}/replays`)
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "replay-of-succeeded")
      .send({ range_start: "2020-01-01T00:00:00.000Z", range_end: "2030-01-01T00:00:00.000Z" });
    expect(replay.status).toBe(202);

    await runReplayExpansionCycle(pool);
    await deliverOnce();

    // The receiver never re-ran its business logic — attempt count for this
    // event_id is unchanged, even though a real HTTP request reached it.
    expect(receiver.attemptsByEventId.get(eventId)).toBe(2);

    const { rows } = await pool.query<{ state: string }>(
      "SELECT state FROM deliveries WHERE event_id = $1 ORDER BY seq DESC LIMIT 1",
      [eventId],
    );
    expect(rows[0]!.state).toBe("succeeded");
  });

  it("a replay of an event that never succeeded is genuinely reprocessed, not silently skipped", async () => {
    let allowSuccess = false;
    const receiver = createConformingReceiver(() => allowSuccess);
    const port = await testServer.listen(receiver.handler);

    const { apiKey, id: tenantId } = await createTenant();
    const app = createApp();
    const register = await request(app)
      .post("/endpoints")
      .set("Authorization", `Bearer ${apiKey}`)
      .send({ url: `http://example.com:${port}/hook`, event_types: ["order.created"] });
    const endpointId: string = register.body.id;

    const { eventId } = await createDelivery(tenantId, endpointId);

    // Exhausts the attempt ceiling — the receiver never succeeds, so it's
    // never marked processed. (BACKOFF_MAX_ATTEMPTS defaults to 6 in tests.)
    for (let i = 0; i < 6; i++) {
      await deliverOnce();
      await new Promise((r) => setTimeout(r, 150));
    }
    expect(receiver.processedEventIds.has(eventId)).toBe(false);

    const { rows: haltedRows } = await pool.query<{ status: string }>("SELECT status FROM endpoints WHERE id = $1", [endpointId]);
    expect(haltedRows[0]!.status).toBe("halted");

    await request(app).post(`/endpoints/${endpointId}/resume`).set("Authorization", `Bearer ${apiKey}`);

    // "Someone fixes the downstream bug" — now it can actually succeed.
    allowSuccess = true;
    const replay = await request(app)
      .post(`/endpoints/${endpointId}/replays`)
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "replay-of-failed")
      .send({ range_start: "2020-01-01T00:00:00.000Z", range_end: "2030-01-01T00:00:00.000Z" });
    expect(replay.status).toBe(202);

    await runReplayExpansionCycle(pool);
    await deliverOnce();

    // A conforming receiver actually ran its business logic again — a
    // receiver that (incorrectly) deduped on mere receipt would never have
    // reached this point, since it would have "seen" this event_id already
    // during the six failed attempts above.
    expect(receiver.processedEventIds.has(eventId)).toBe(true);

    const { rows } = await pool.query<{ state: string }>(
      "SELECT state FROM deliveries WHERE event_id = $1 ORDER BY seq DESC LIMIT 1",
      [eventId],
    );
    expect(rows[0]!.state).toBe("succeeded");
  });
});
