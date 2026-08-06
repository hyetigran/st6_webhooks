import { describe, it, expect } from "vitest";
import request from "supertest";
import { createApp } from "../../src/app.js";
import { pool } from "../../src/db/pool.js";
import { runExpansionCycle } from "../../src/worker/expansion.js";
import { createTenant, createEndpoint } from "../fixtures.js";
import { mulberry32, getTestSeed, randomInt } from "./prng.js";

async function drainExpansion(): Promise<void> {
  while (await runExpansionCycle(pool)) {
    /* keep draining */
  }
}

// PRD §8: "Repeated publish and replay keys create no additional
// deliveries" (R-6/R-21). Randomizes how many times the same key is
// replayed (retried request, network retry, at-least-once caller, etc.) —
// the invariant must hold regardless of the repeat count.
describe("repeated publish key property: no additional deliveries beyond the first", () => {
  it("holds for a randomized number of repeated POST /events calls with the same Idempotency-Key", async () => {
    const seed = getTestSeed("repeated-publish-key");
    const rng = mulberry32(seed);
    const repeatCount = randomInt(rng, 2, 10);

    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const app = createApp();

    const idempotencyKey = `repeat-property-${seed}`;
    const eventIds = new Set<string>();
    for (let i = 0; i < repeatCount; i++) {
      const res = await request(app)
        .post("/events")
        .set("Authorization", `Bearer ${apiKey}`)
        .set("Idempotency-Key", idempotencyKey)
        .send({ type: "order.created", payload: { attempt: i } });
      expect(res.status).toBe(202);
      eventIds.add(res.body.id);
    }

    expect(eventIds.size).toBe(1); // every repeat resolved to the same event

    await drainExpansion();

    const { rows } = await pool.query<{ count: string }>(
      "SELECT count(*) FROM deliveries WHERE endpoint_id = $1",
      [endpoint.id],
    );
    expect(Number(rows[0]!.count)).toBe(1); // exactly one fan-out, regardless of repeatCount
  });
});
