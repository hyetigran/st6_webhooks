import { describe, it, expect, afterEach, vi } from "vitest";
import { type IncomingMessage } from "node:http";
import { createHmac } from "node:crypto";
import { pool } from "../src/db/pool.js";
import { runDeliveryCycle } from "../src/worker/delivery.js";
import { resolveAndPin } from "../src/worker/httpClient.js";
import { createTenant, createEndpoint, createPendingDelivery } from "./fixtures.js";
import { createTestServerHarness } from "./testServer.js";

const testServer = createTestServerHarness();
afterEach(() => testServer.close());
const listen = testServer.listen;

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve) => {
    let body = "";
    req.on("data", (chunk) => (body += chunk));
    req.on("end", () => resolve(body));
  });
}

// The real SSRF check (resolveAndPin) always rejects loopback (ADR-0006) —
// correctly, since that's exactly the class of address it exists to block.
// A local test receiver is necessarily on loopback, so there is no DNS
// answer that both routes there and passes validation. This bypasses
// resolveAndPin wholesale for this one orchestration test; the real
// validate-then-pin logic (including its stub-resolver "rebinding" fixture)
// has its own dedicated coverage in httpClient.resolveAndPin.test.ts and in
// the "rejects delivery to a private range" test below, which does not
// bypass it.
function trustPinnedLoopback(): (hostname: string) => Promise<{ allowed: true; ip: string }> {
  return async () => ({ allowed: true, ip: "127.0.0.1" });
}

describe("runDeliveryCycle", () => {
  it("returns false when there is nothing to deliver", async () => {
    const didWork = await runDeliveryCycle(pool);
    expect(didWork).toBe(false);
  });

  it("claims, signs, sends, and marks a delivery succeeded end-to-end against a real receiver", async () => {
    let receivedHeaders: Record<string, string | string[] | undefined> = {};
    let receivedBody = "";
    const port = await listen(async (req, res) => {
      receivedHeaders = req.headers;
      receivedBody = await readBody(req);
      res.writeHead(200);
      res.end();
    });

    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"], {
      url: `http://delivery-worker-test.invalid:${port}/webhook`,
      signingSecret: "whsec_test_secret",
    });
    const { id: deliveryId, eventId } = await createPendingDelivery(tenantId, endpoint.id, {
      eventType: "order.created",
      payload: { orderId: "abc123" },
    });

    const didWork = await runDeliveryCycle(pool, { resolveAndPin: trustPinnedLoopback() });

    expect(didWork).toBe(true);
    expect(receivedBody).toBe(JSON.stringify({ orderId: "abc123" }));
    expect(receivedHeaders["webhook-id"]).toBe(deliveryId);
    expect(receivedHeaders["webhook-event-id"]).toBe(eventId);
    expect(receivedHeaders["webhook-attempt"]).toBe("1");
    expect(typeof receivedHeaders["webhook-timestamp"]).toBe("string");

    const expectedSignature = createHmac("sha256", "whsec_test_secret")
      .update(`${receivedHeaders["webhook-timestamp"]}.${receivedBody}`, "utf8")
      .digest("hex");
    expect(receivedHeaders["webhook-signature"]).toBe(expectedSignature);

    const { rows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [deliveryId]);
    expect(rows[0]!.state).toBe("succeeded");
  });

  it("signs with both the current and secondary secret during a rotation overlap window (ADR-0003)", async () => {
    let receivedHeaders: Record<string, string | string[] | undefined> = {};
    let receivedBody = "";
    const port = await listen(async (req, res) => {
      receivedHeaders = req.headers;
      receivedBody = await readBody(req);
      res.writeHead(200);
      res.end();
    });

    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"], {
      url: `http://delivery-worker-test.invalid:${port}/webhook`,
      signingSecret: "whsec_new",
      secondarySecret: "whsec_old",
      secondarySecretExpiresAt: new Date(Date.now() + 60_000),
    });
    await createPendingDelivery(tenantId, endpoint.id, { payload: { orderId: "rotation-test" } });

    await runDeliveryCycle(pool, { resolveAndPin: trustPinnedLoopback() });

    const timestamp = receivedHeaders["webhook-timestamp"];
    const expectedSignatures = [
      createHmac("sha256", "whsec_new").update(`${timestamp}.${receivedBody}`, "utf8").digest("hex"),
      createHmac("sha256", "whsec_old").update(`${timestamp}.${receivedBody}`, "utf8").digest("hex"),
    ].join(",");
    expect(receivedHeaders["webhook-signature"]).toBe(expectedSignatures);
  });

  it("rejects delivery to an address that resolves to a private range, without ever connecting", async () => {
    const connectSpy = vi.fn();
    const { id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"], { url: "http://rebinding-host.invalid:1/webhook" });
    const { id: deliveryId } = await createPendingDelivery(tenantId, endpoint.id);

    const rebindingResolver = async (h: string) => {
      connectSpy(h);
      return [{ address: "169.254.169.254", family: 4 }];
    };

    const didWork = await runDeliveryCycle(pool, {
      resolveAndPin: (hostname) => resolveAndPin(hostname, rebindingResolver),
    });

    expect(didWork).toBe(true); // a cycle ran — it just didn't succeed
    expect(connectSpy).toHaveBeenCalledTimes(1);

    const { rows: deliveryRows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [deliveryId]);
    expect(deliveryRows[0]!.state).toBe("pending"); // rescheduled for retry, not stuck in_flight

    const { rows: attemptRows } = await pool.query<{ error_class: string | null }>(
      "SELECT error_class FROM attempts WHERE delivery_id = $1",
      [deliveryId],
    );
    expect(attemptRows[0]!.error_class).toBe("url_not_allowed");
  });
});
