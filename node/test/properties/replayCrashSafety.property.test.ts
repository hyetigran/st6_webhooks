import { describe, it, expect } from "vitest";
import request from "supertest";
import { createApp } from "../../src/app.js";
import { pool } from "../../src/db/pool.js";
import { runExpansionCycle } from "../../src/worker/expansion.js";
import { runReplayExpansionCycle } from "../../src/worker/replayExpansion.js";
import { createTenant, createEndpoint } from "../fixtures.js";
import { mulberry32, getTestSeed, randomInt } from "./prng.js";

async function drainExpansion(): Promise<void> {
  while (await runExpansionCycle(pool)) {
    /* keep draining */
  }
}

// REVIEW.md F-8 / docs/adr/0005: "crash-inject between a replay's durable
// ack and its expansion; retry the same key; deliveries created exactly
// once, in original order" (R-19/R-21/R-22). The "crash" is exactly the gap
// between the POST committing the replays row and the worker expanding it —
// retrying the POST any number of times in that gap must never double the
// result once expansion finally runs. Randomizes how many retries land in
// that gap.
describe("replay crash-safety property: retries before expansion still create deliveries exactly once", () => {
  it("holds for a randomized number of retried POST /endpoints/:id/replays calls before the worker expands it", async () => {
    const seed = getTestSeed("replay-crash-safety");
    const rng = mulberry32(seed);
    const retryCount = randomInt(rng, 2, 8);

    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const app = createApp();

    // A terminal original delivery, in the replay window, for the worker to
    // find once expansion runs.
    const publish = await request(app)
      .post("/events")
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "replay-crash-safety-original")
      .send({ type: "order.created", payload: {} });
    await drainExpansion();
    await pool.query("UPDATE deliveries SET state = 'succeeded' WHERE event_id = $1", [publish.body.id]);

    const idempotencyKey = `replay-crash-safety-${seed}`;
    const replayIds = new Set<string>();
    // Simulates the crash-and-retry window: N requests all land before the
    // worker ever runs expansion for this replay.
    for (let i = 0; i < retryCount; i++) {
      const res = await request(app)
        .post(`/endpoints/${endpoint.id}/replays`)
        .set("Authorization", `Bearer ${apiKey}`)
        .set("Idempotency-Key", idempotencyKey)
        .send({ range_start: "2020-01-01T00:00:00.000Z", range_end: "2030-01-01T00:00:00.000Z" });
      expect(res.status).toBe(202);
      replayIds.add(res.body.id);
    }
    expect(replayIds.size).toBe(1); // every retry resolved to the same replays row

    await runReplayExpansionCycle(pool);

    const { rows } = await pool.query<{ count: string }>(
      "SELECT count(*) FROM deliveries WHERE endpoint_id = $1 AND event_id = $2",
      [endpoint.id, publish.body.id],
    );
    // The one original delivery (already 'succeeded' above) plus exactly
    // one replayed delivery — never more, regardless of retryCount.
    expect(Number(rows[0]!.count)).toBe(2);
  });
});
