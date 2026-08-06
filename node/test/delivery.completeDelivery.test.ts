import { describe, it, expect } from "vitest";
import { pool } from "../src/db/pool.js";
import { claimDelivery, completeDelivery, type ClaimedDelivery } from "../src/worker/delivery.js";
import { createTenant, createEndpoint, createPendingDelivery } from "./fixtures.js";

const LEASE_DURATION_MS = 60_000;
const backoffConfig = { baseDelayMs: 1_000, multiplier: 2, maxDelayMs: 30_000, maxAttempts: 6 };

async function setUpClaimedDelivery(): Promise<{ claimed: ClaimedDelivery; endpointId: string; deliveryId: string }> {
  const { id: tenantId } = await createTenant();
  const endpoint = await createEndpoint(tenantId, ["order.created"]);
  const { id: deliveryId } = await createPendingDelivery(tenantId, endpoint.id);
  const claimed = await claimDelivery(pool, LEASE_DURATION_MS);
  return { claimed: claimed!, endpointId: endpoint.id, deliveryId };
}

describe("completeDelivery", () => {
  it("marks the delivery succeeded and releases the endpoint on a 2xx response", async () => {
    const { claimed, endpointId, deliveryId } = await setUpClaimedDelivery();

    const outcome = await completeDelivery(
      pool,
      claimed,
      { responseStatus: 200, responseBodyTruncated: "ok", durationMs: 42, errorClass: null },
      backoffConfig,
    );

    expect(outcome).toBe("succeeded");

    const { rows: deliveryRows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [deliveryId]);
    expect(deliveryRows[0]!.state).toBe("succeeded");

    const { rows: attemptRows } = await pool.query<{ response_status: number; duration_ms: number }>(
      "SELECT response_status, duration_ms FROM attempts WHERE id = $1",
      [claimed.attemptId],
    );
    expect(attemptRows[0]).toMatchObject({ response_status: 200, duration_ms: 42 });

    const { rows: endpointRows } = await pool.query<{ busy: boolean; lease_id: string | null; status: string }>(
      "SELECT busy, lease_id, status FROM endpoints WHERE id = $1",
      [endpointId],
    );
    expect(endpointRows[0]).toMatchObject({ busy: false, lease_id: null, status: "active" });
  });

  it("reschedules the delivery with a future next_attempt_at on a non-2xx response, below the attempt ceiling", async () => {
    const { claimed, endpointId, deliveryId } = await setUpClaimedDelivery();

    const outcome = await completeDelivery(
      pool,
      claimed,
      { responseStatus: 500, responseBodyTruncated: "server error", durationMs: 10, errorClass: null },
      backoffConfig,
    );

    expect(outcome).toBe("retrying");

    const { rows: deliveryRows } = await pool.query<{ state: string; next_attempt_at: Date }>(
      "SELECT state, next_attempt_at FROM deliveries WHERE id = $1",
      [deliveryId],
    );
    expect(deliveryRows[0]!.state).toBe("pending");
    expect(deliveryRows[0]!.next_attempt_at.getTime()).toBeGreaterThanOrEqual(Date.now());

    const { rows: endpointRows } = await pool.query<{ busy: boolean; status: string }>(
      "SELECT busy, status FROM endpoints WHERE id = $1",
      [endpointId],
    );
    expect(endpointRows[0]).toMatchObject({ busy: false, status: "active" });
  });

  it("treats a network-level failure (no response) the same as a non-2xx for retry purposes", async () => {
    const { claimed, deliveryId } = await setUpClaimedDelivery();

    const outcome = await completeDelivery(
      pool,
      claimed,
      { responseStatus: null, responseBodyTruncated: "", durationMs: 5_000, errorClass: "total_timeout" },
      backoffConfig,
    );

    expect(outcome).toBe("retrying");
    const { rows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [deliveryId]);
    expect(rows[0]!.state).toBe("pending");
  });

  it("halts the endpoint and marks the delivery failed once the attempt ceiling is reached", async () => {
    const { claimed, endpointId, deliveryId } = await setUpClaimedDelivery();
    // Force this to be the ceiling attempt without needing 6 real round trips.
    const claimedAtCeiling = { ...claimed, attemptNumber: backoffConfig.maxAttempts };

    const outcome = await completeDelivery(
      pool,
      claimedAtCeiling,
      { responseStatus: 500, responseBodyTruncated: "still failing", durationMs: 10, errorClass: null },
      backoffConfig,
    );

    expect(outcome).toBe("halted");

    const { rows: deliveryRows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [deliveryId]);
    expect(deliveryRows[0]!.state).toBe("failed");

    const { rows: endpointRows } = await pool.query<{ status: string; busy: boolean }>(
      "SELECT status, busy FROM endpoints WHERE id = $1",
      [endpointId],
    );
    expect(endpointRows[0]).toMatchObject({ status: "halted", busy: false });
  });

  it("silently drops the write-back when the lease no longer matches (another worker has since reclaimed the endpoint)", async () => {
    const { claimed, endpointId, deliveryId } = await setUpClaimedDelivery();

    // Simulate another worker reclaiming this endpoint after our lease
    // expired — its lease_id no longer matches what we captured at claim.
    await pool.query("UPDATE endpoints SET lease_id = gen_random_uuid() WHERE id = $1", [endpointId]);

    const outcome = await completeDelivery(
      pool,
      claimed,
      { responseStatus: 200, responseBodyTruncated: "ok", durationMs: 42, errorClass: null },
      backoffConfig,
    );

    expect(outcome).toBe("lease_lost");

    // Our write-back must not have touched the delivery the new owner is
    // now responsible for.
    const { rows: deliveryRows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [deliveryId]);
    expect(deliveryRows[0]!.state).toBe("in_flight");
  });
});
