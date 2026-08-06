import { describe, it, expect } from "vitest";
import request from "supertest";
import { createApp } from "../src/app.js";
import { createTenant } from "./fixtures.js";

describe("POST /events", () => {
  it("publishes an event and returns 202 with a fresh id and pending_expansion status", async () => {
    const { apiKey } = await createTenant();
    const app = createApp();

    const res = await request(app)
      .post("/events")
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "publish-key-1")
      .send({ type: "order.created", payload: { orderId: "abc123" } });

    expect(res.status).toBe(202);
    expect(res.body).toMatchObject({ status: "pending_expansion" });
    expect(typeof res.body.id).toBe("string");
    expect(res.body.id.length).toBeGreaterThan(0);
  });

  it("returns the original event's id when the same Idempotency-Key is reused", async () => {
    const { apiKey } = await createTenant();
    const app = createApp();

    const first = await request(app)
      .post("/events")
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "repeat-key")
      .send({ type: "order.created", payload: { orderId: "abc123" } });

    const second = await request(app)
      .post("/events")
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "repeat-key")
      .send({ type: "order.created", payload: { orderId: "abc123" } });

    expect(second.status).toBe(202);
    expect(second.body.id).toBe(first.body.id);
    expect(second.body.status).toBe(first.body.status);
  });

  it("rejects a publish with no Idempotency-Key header", async () => {
    const { apiKey } = await createTenant();
    const app = createApp();

    const res = await request(app)
      .post("/events")
      .set("Authorization", `Bearer ${apiKey}`)
      .send({ type: "order.created", payload: {} });

    expect(res.status).toBe(400);
  });

  it("rejects a publish with an invalid body", async () => {
    const { apiKey } = await createTenant();
    const app = createApp();

    const res = await request(app)
      .post("/events")
      .set("Authorization", `Bearer ${apiKey}`)
      .set("Idempotency-Key", "some-key")
      .send({ payload: {} }); // missing required `type`

    expect(res.status).toBe(400);
  });
});
