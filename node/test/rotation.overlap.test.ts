import { describe, it, expect, afterEach } from "vitest";
import request from "supertest";
import { createHmac } from "node:crypto";
import { createApp } from "../src/app.js";
import { pool } from "../src/db/pool.js";
import { runDeliveryCycle } from "../src/worker/delivery.js";
import { createTenant, createDelivery } from "./fixtures.js";
import { createTestServerHarness } from "./testServer.js";

// REVIEW.md F-3: the sender must sign with every still-active secret during
// a rotation overlap window, not just the current one — a receiver that's
// only caught up to the old secret must keep verifying successfully
// throughout the window, and deliveries must never halt on account of a
// rotation. This is the closing "make test" scenario named in F-3's
// resolution.

const testServer = createTestServerHarness();
afterEach(() => testServer.close());

function verifiesWith(secret: string, timestamp: string, body: string, signatureHeader: string): boolean {
  const expected = createHmac("sha256", secret).update(`${timestamp}.${body}`, "utf8").digest("hex");
  return signatureHeader.split(",").includes(expected);
}

describe("secret rotation overlap (F-3)", () => {
  it("keeps verifying against the old secret throughout the overlap window; deliveries never halt", async () => {
    let receivedHeaders: Record<string, string | string[] | undefined> = {};
    let receivedBody = "";
    const port = await testServer.listen(async (req, res) => {
      receivedHeaders = req.headers;
      let body = "";
      req.on("data", (c) => (body += c));
      req.on("end", () => {
        receivedBody = body;
        res.writeHead(200);
        res.end();
      });
    });

    const { apiKey, id: tenantId } = await createTenant();
    const app = createApp();
    const register = await request(app)
      .post("/endpoints")
      .set("Authorization", `Bearer ${apiKey}`)
      .send({ url: `http://example.com:${port}/hook`, event_types: ["order.created"] });
    const endpointId: string = register.body.id;
    const oldSecret: string = register.body.signing_secret;

    const rotate = await request(app).post(`/endpoints/${endpointId}/secret/rotate`).set("Authorization", `Bearer ${apiKey}`);
    expect(rotate.status).toBe(200);
    const newSecret: string = rotate.body.signing_secret;

    await createDelivery(tenantId, endpointId, { payload: { during: "overlap" } });
    const didWork = await runDeliveryCycle(pool, { resolveAndPin: async () => ({ allowed: true, ip: "127.0.0.1" }) });

    expect(didWork).toBe(true);
    const timestamp = receivedHeaders["webhook-timestamp"] as string;
    const signatureHeader = receivedHeaders["webhook-signature"] as string;
    expect(verifiesWith(oldSecret, timestamp, receivedBody, signatureHeader)).toBe(true);
    expect(verifiesWith(newSecret, timestamp, receivedBody, signatureHeader)).toBe(true);

    const { rows } = await pool.query<{ status: string }>("SELECT status FROM endpoints WHERE id = $1", [endpointId]);
    expect(rows[0]!.status).toBe("active"); // never halted on account of rotation
  });

  it("stops signing with the old secret once its overlap window has expired", async () => {
    let receivedHeaders: Record<string, string | string[] | undefined> = {};
    let receivedBody = "";
    const port = await testServer.listen(async (req, res) => {
      receivedHeaders = req.headers;
      let body = "";
      req.on("data", (c) => (body += c));
      req.on("end", () => {
        receivedBody = body;
        res.writeHead(200);
        res.end();
      });
    });

    const { apiKey, id: tenantId } = await createTenant();
    const app = createApp();
    const register = await request(app)
      .post("/endpoints")
      .set("Authorization", `Bearer ${apiKey}`)
      .send({ url: `http://example.com:${port}/hook`, event_types: ["order.created"] });
    const endpointId: string = register.body.id;
    const oldSecret: string = register.body.signing_secret;

    await request(app).post(`/endpoints/${endpointId}/secret/rotate`).set("Authorization", `Bearer ${apiKey}`);
    // Simulate the overlap window having already elapsed.
    await pool.query("UPDATE endpoints SET secondary_secret_expires_at = now() - interval '1 hour' WHERE id = $1", [endpointId]);

    await createDelivery(tenantId, endpointId, { payload: { after: "expiry" } });
    await runDeliveryCycle(pool, { resolveAndPin: async () => ({ allowed: true, ip: "127.0.0.1" }) });

    const timestamp = receivedHeaders["webhook-timestamp"] as string;
    const signatureHeader = receivedHeaders["webhook-signature"] as string;
    expect(verifiesWith(oldSecret, timestamp, receivedBody, signatureHeader)).toBe(false);
  });
});
