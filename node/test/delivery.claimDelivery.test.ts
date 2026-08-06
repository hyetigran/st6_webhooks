import { describe, it, expect } from "vitest";
import { pool } from "../src/db/pool.js";
import { claimDelivery } from "../src/worker/delivery.js";
import { createTenant, createEndpoint, createDelivery } from "./fixtures.js";

const LEASE_DURATION_MS = 60_000;

describe("claimDelivery", () => {
  it("claims the endpoint's oldest pending delivery, marks it in_flight, and inserts a sent attempt row", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"], { url: "https://example.com/hook", signingSecret: "whsec_abc" });
    const { id: deliveryId, eventId } = await createDelivery(tenantId, endpoint.id, {
      eventType: "order.created",
      payload: { orderId: "123" },
    });

    const claimed = await claimDelivery(pool, LEASE_DURATION_MS);

    expect(claimed).toMatchObject({
      endpointId: endpoint.id,
      tenantId,
      deliveryId,
      eventId,
      eventType: "order.created",
      payload: { orderId: "123" },
      attemptNumber: 1,
      url: "https://example.com/hook",
      signingSecret: "whsec_abc",
      secondarySecret: null,
    });
    expect(typeof claimed!.leaseId).toBe("string");

    const { rows: deliveryRows } = await pool.query<{ state: string; attempt_count: number }>(
      "SELECT state, attempt_count FROM deliveries WHERE id = $1",
      [deliveryId],
    );
    expect(deliveryRows[0]).toMatchObject({ state: "in_flight", attempt_count: 1 });

    const { rows: attemptRows } = await pool.query<{ attempt_number: number; sent_at: Date | null; response_status: number | null }>(
      "SELECT attempt_number, sent_at, response_status FROM attempts WHERE delivery_id = $1",
      [deliveryId],
    );
    expect(attemptRows).toHaveLength(1);
    expect(attemptRows[0]!.attempt_number).toBe(1);
    expect(attemptRows[0]!.sent_at).not.toBeNull();
    expect(attemptRows[0]!.response_status).toBeNull();

    const { rows: endpointRows } = await pool.query<{ busy: boolean; lease_id: string | null }>(
      "SELECT busy, lease_id FROM endpoints WHERE id = $1",
      [endpoint.id],
    );
    expect(endpointRows[0]!.busy).toBe(true);
    expect(endpointRows[0]!.lease_id).toBe(claimed!.leaseId);
  });

  it("updates the claimed endpoint's tenant last_served_at", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    await createDelivery(tenantId, endpoint.id);

    await claimDelivery(pool, LEASE_DURATION_MS);

    const { rows } = await pool.query<{ last_served_at: Date | null }>("SELECT last_served_at FROM tenants WHERE id = $1", [tenantId]);
    expect(rows[0]!.last_served_at).not.toBeNull();
  });

  it("prefers the tenant that has gone longest unserved (last_served_at ASC, NULL first)", async () => {
    const quietTenant = await createTenant("quiet");
    const servedTenant = await createTenant("served");
    await pool.query("UPDATE tenants SET last_served_at = now() WHERE id = $1", [servedTenant.id]);

    const quietEndpoint = await createEndpoint(quietTenant.id, ["order.created"]);
    const servedEndpoint = await createEndpoint(servedTenant.id, ["order.created"]);
    await createDelivery(quietTenant.id, quietEndpoint.id);
    await createDelivery(servedTenant.id, servedEndpoint.id);

    const claimed = await claimDelivery(pool, LEASE_DURATION_MS);

    expect(claimed!.endpointId).toBe(quietEndpoint.id);
  });

  it("never jumps ahead to a later, immediately-eligible delivery while the head is still cooling down from backoff (R-11)", async () => {
    // Found via chaos testing (make chaos's partition-head scenario): an
    // earlier version of the delivery-pick query filtered on
    // next_attempt_at <= now(), which let it skip past an ineligible head
    // to grab a later delivery that happened to be ready — violating strict
    // per-endpoint order the moment a head is mid-backoff, not just when a
    // worker is stalled or dead.
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const head = await createDelivery(tenantId, endpoint.id, { nextAttemptAt: new Date(Date.now() + 60_000) });
    const follower = await createDelivery(tenantId, endpoint.id); // next_attempt_at defaults to now — immediately eligible

    const claimed = await claimDelivery(pool, LEASE_DURATION_MS);

    expect(claimed).toBeNull();
    const { rows } = await pool.query<{ id: string; state: string }>("SELECT id, state FROM deliveries WHERE id = ANY($1)", [
      [head.id, follower.id],
    ]);
    expect(rows.every((r) => r.state === "pending")).toBe(true); // neither was claimed

    // Once the head's cooldown actually elapses, it — not the follower —
    // is what gets claimed next.
    await pool.query("UPDATE deliveries SET next_attempt_at = now() WHERE id = $1", [head.id]);
    const claimedAfterCooldown = await claimDelivery(pool, LEASE_DURATION_MS);
    expect(claimedAfterCooldown!.deliveryId).toBe(head.id);
  });

  it("never claims a paused endpoint's delivery, even though it accumulates (R-4)", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"], { status: "paused" });
    const delivery = await createDelivery(tenantId, endpoint.id);

    const claimed = await claimDelivery(pool, LEASE_DURATION_MS);

    expect(claimed).toBeNull();
    const { rows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [delivery.id]);
    expect(rows[0]!.state).toBe("pending"); // accumulated, not discarded
  });

  it("returns null when the only pending delivery's endpoint is already busy within its lease", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    await createDelivery(tenantId, endpoint.id);
    await pool.query("UPDATE endpoints SET busy = true, busy_since = now(), lease_id = gen_random_uuid() WHERE id = $1", [endpoint.id]);

    const claimed = await claimDelivery(pool, LEASE_DURATION_MS);

    expect(claimed).toBeNull();
  });

  it("returns null when there is nothing pending to claim", async () => {
    const claimed = await claimDelivery(pool, LEASE_DURATION_MS);
    expect(claimed).toBeNull();
  });

  it("reclaims a stale-leased endpoint: closes the orphaned in-flight attempt and requeues its delivery as the new claim", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const { id: deliveryId } = await createDelivery(tenantId, endpoint.id);

    // Simulate a worker that claimed this delivery, sent an attempt, then
    // died before writing back — busy_since is far enough in the past that
    // its lease has expired.
    const staleBusySince = new Date(Date.now() - LEASE_DURATION_MS * 2);
    await pool.query(
      "UPDATE endpoints SET busy = true, busy_since = $2, lease_id = gen_random_uuid() WHERE id = $1",
      [endpoint.id, staleBusySince],
    );
    await pool.query("UPDATE deliveries SET state = 'in_flight', attempt_count = 1 WHERE id = $1", [deliveryId]);
    const { rows: orphanedAttempt } = await pool.query<{ id: string }>(
      "INSERT INTO attempts (delivery_id, attempt_number, sent_at) VALUES ($1, 1, now() - interval '1 hour') RETURNING id",
      [deliveryId],
    );

    const claimed = await claimDelivery(pool, LEASE_DURATION_MS);

    expect(claimed).toMatchObject({ deliveryId, attemptNumber: 2 });

    const { rows: closedAttemptRows } = await pool.query<{ error_class: string | null }>(
      "SELECT error_class FROM attempts WHERE id = $1",
      [orphanedAttempt[0]!.id],
    );
    expect(closedAttemptRows[0]!.error_class).toBe("worker_lease_expired");
  });

  it("includes the decrypted secondary secret when a rotation overlap window is still active", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"], {
      signingSecret: "whsec_new",
      secondarySecret: "whsec_old",
      secondarySecretExpiresAt: new Date(Date.now() + 60_000),
    });
    await createDelivery(tenantId, endpoint.id);

    const claimed = await claimDelivery(pool, LEASE_DURATION_MS);

    expect(claimed).toMatchObject({ signingSecret: "whsec_new", secondarySecret: "whsec_old" });
  });

  it("omits the secondary secret once its rotation overlap window has expired", async () => {
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"], {
      signingSecret: "whsec_new",
      secondarySecret: "whsec_old",
      secondarySecretExpiresAt: new Date(Date.now() - 60_000),
    });
    await createDelivery(tenantId, endpoint.id);

    const claimed = await claimDelivery(pool, LEASE_DURATION_MS);

    expect(claimed!.secondarySecret).toBeNull();
  });

  it("claims two different endpoints concurrently without either losing its claim to the other", async () => {
    const { id: tenantId } = await createTenant();
    const endpointA = await createEndpoint(tenantId, ["order.created"]);
    const endpointB = await createEndpoint(tenantId, ["order.created"]);
    await createDelivery(tenantId, endpointA.id);
    await createDelivery(tenantId, endpointB.id);

    const [first, second] = await Promise.all([
      claimDelivery(pool, LEASE_DURATION_MS),
      claimDelivery(pool, LEASE_DURATION_MS),
    ]);

    const claimedEndpointIds = [first!.endpointId, second!.endpointId].sort();
    expect(claimedEndpointIds).toEqual([endpointA.id, endpointB.id].sort());
  });
});
