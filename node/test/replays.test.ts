import { describe, it, expect } from "vitest";
import request from "supertest";
import { createApp } from "../src/app.js";
import { createTenant, createEndpoint } from "./fixtures.js";

const validBody = { range_start: "2026-01-01T00:00:00.000Z", range_end: "2026-01-02T00:00:00.000Z" };

describe("POST /endpoints/:id/replays", () => {
  it("accepts a replay request and returns 202 with a fresh id and pending_expansion status", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const app = createApp();

    const res = await request(app)
      .post(`/endpoints/${endpoint.id}/replays`)
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "replay-key-1")
      .send(validBody);

    expect(res.status).toBe(202);
    expect(res.body).toMatchObject({ status: "pending_expansion" });
    expect(typeof res.body.id).toBe("string");
  });

  it("returns the original replay's id when the same Idempotency-Key is reused", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const app = createApp();

    const first = await request(app)
      .post(`/endpoints/${endpoint.id}/replays`)
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "repeat-replay-key")
      .send(validBody);

    const second = await request(app)
      .post(`/endpoints/${endpoint.id}/replays`)
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "repeat-replay-key")
      .send(validBody);

    expect(second.status).toBe(202);
    expect(second.body.id).toBe(first.body.id);
  });

  it("rejects a replay request with no Idempotency-Key header", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const app = createApp();

    const res = await request(app)
      .post(`/endpoints/${endpoint.id}/replays`)
      .set("Authorization", `Bearer ${apiKey}`)
      .send(validBody);

    expect(res.status).toBe(400);
  });

  it("rejects a replay request with range_end before range_start", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const app = createApp();

    const res = await request(app)
      .post(`/endpoints/${endpoint.id}/replays`)
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "reversed-range")
      .send({ range_start: "2026-01-02T00:00:00.000Z", range_end: "2026-01-01T00:00:00.000Z" });

    expect(res.status).toBe(400);
  });

  it("returns 404 for an endpoint that doesn't exist", async () => {
    const { apiKey } = await createTenant();
    const app = createApp();

    const res = await request(app)
      .post(`/endpoints/00000000-0000-0000-0000-000000000000/replays`)
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "missing-endpoint")
      .send(validBody);

    expect(res.status).toBe(404);
  });

  it("returns 404 for an endpoint belonging to a different tenant", async () => {
    const { id: otherTenantId } = await createTenant("other-tenant");
    const foreignEndpoint = await createEndpoint(otherTenantId, ["order.created"]);
    const { apiKey } = await createTenant();
    const app = createApp();

    const res = await request(app)
      .post(`/endpoints/${foreignEndpoint.id}/replays`)
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "cross-tenant")
      .send(validBody);

    expect(res.status).toBe(404);
  });
});
