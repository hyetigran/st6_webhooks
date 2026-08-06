import { describe, it, expect } from "vitest";
import { pool } from "../src/db/pool.js";
import { claimDelivery, completeDelivery, type BackoffConfig } from "../src/worker/delivery.js";
import { createTenant, createEndpoint, createDelivery } from "./fixtures.js";

// PRD §8: "Reconstruct the backoff schedule from attempts timestamps;
// matches the stated formula; endpoint halts on the final failure, not a
// later claim." Small config values so the whole schedule runs in
// well under a second instead of the ~31-61s the production defaults would
// take — completeDelivery takes backoffConfig as an explicit parameter
// specifically so tests don't have to fight the real global config's timing.
const LEASE_DURATION_MS = 60_000;
const backoffConfig: BackoffConfig = { baseDelayMs: 20, multiplier: 2, maxDelayMs: 200, maxAttempts: 4 };

describe("backoff schedule (R-13/R-14)", () => {
  it("schedules each retry within the formula's ceiling and halts exactly on the maxAttempts-th failure", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const delivery = await createDelivery(tenantId, endpoint.id, { state: "pending" });

    for (let attempt = 1; attempt <= backoffConfig.maxAttempts; attempt++) {
      const claimed = await claimDelivery(pool, LEASE_DURATION_MS);
      expect(claimed, `expected a claimable delivery at attempt ${attempt}`).not.toBeNull();

      const beforeCompleteAt = Date.now();
      const outcome = await completeDelivery(
        pool,
        claimed!,
        { responseStatus: 500, responseBodyTruncated: "server error", durationMs: 5, errorClass: null },
        backoffConfig,
      );

      if (attempt < backoffConfig.maxAttempts) {
        expect(outcome, `attempt ${attempt} should retry, not halt`).toBe("retrying");

        // Check the *scheduled* delay (next_attempt_at) right after it was
        // set, not the wall-clock gap between attempts — this loop's own
        // polling cadence below would otherwise dominate that measurement
        // and it wouldn't be testing the formula at all.
        const { rows } = await pool.query<{ next_attempt_at: Date }>("SELECT next_attempt_at FROM deliveries WHERE id = $1", [
          delivery.id,
        ]);
        const scheduledDelayMs = rows[0]!.next_attempt_at.getTime() - beforeCompleteAt;
        const ceiling = Math.min(backoffConfig.baseDelayMs * backoffConfig.multiplier ** (attempt - 1), backoffConfig.maxDelayMs);
        expect(scheduledDelayMs, `attempt ${attempt}'s scheduled delay should be within the formula's ceiling`).toBeGreaterThanOrEqual(
          -50, // small negative tolerance: beforeCompleteAt is measured slightly before the DB's own now()
        );
        expect(scheduledDelayMs).toBeLessThanOrEqual(ceiling + 100);

        // Wait past the largest possible jittered delay (full jitter caps at
        // the ceiling, never exceeds it) so the next claim always finds the
        // delivery ready, regardless of what the random draw happened to be.
        await new Promise((resolve) => setTimeout(resolve, backoffConfig.maxDelayMs + 50));
      } else {
        expect(outcome, "the maxAttempts-th failure should halt, not schedule another retry").toBe("halted");
      }
    }

    const { rows: attempts } = await pool.query<{ attempt_number: number }>(
      "SELECT attempt_number FROM attempts WHERE delivery_id = $1 ORDER BY attempt_number",
      [delivery.id],
    );
    expect(attempts.map((a) => a.attempt_number)).toEqual([1, 2, 3, 4]);

    const { rows: endpointRows } = await pool.query<{ status: string }>("SELECT status FROM endpoints WHERE id = $1", [endpoint.id]);
    expect(endpointRows[0]!.status).toBe("halted");

    // Halts on the final failure, not a later claim: a halted endpoint must
    // never be claimable again, immediately — not just eventually.
    const claimAfterHalt = await claimDelivery(pool, LEASE_DURATION_MS);
    expect(claimAfterHalt).toBeNull();
  });
});
