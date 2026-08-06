import { describe, it, expect } from "vitest";
import request from "supertest";
import { createApp } from "../src/app.js";
import { createTenant, createEndpoint, createDelivery, createAttempt } from "./fixtures.js";

describe("GET /deliveries/:id", () => {
  it("returns the head delivery's detail with no blocked_on_delivery_id", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const delivery = await createDelivery(tenantId, endpoint.id, { state: "pending" });
    const app = createApp();

    const res = await request(app).get(`/deliveries/${delivery.id}`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.status).toBe(200);
    expect(res.body).toMatchObject({
      id: delivery.id,
      event_id: delivery.eventId,
      endpoint_id: endpoint.id,
      state: "pending",
      attempt_count: 0,
      blocked_on_delivery_id: null,
      last_response: null,
      attempts: [],
    });
  });

  it("reports blocked_on_delivery_id for a pending delivery that isn't the endpoint's head", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const head = await createDelivery(tenantId, endpoint.id, { state: "pending" });
    const blocked = await createDelivery(tenantId, endpoint.id, { state: "pending" });
    const app = createApp();

    const res = await request(app).get(`/deliveries/${blocked.id}`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.status).toBe(200);
    expect(res.body.blocked_on_delivery_id).toBe(head.id);
  });

  it("is never blocked while in_flight, even though it was necessarily the head to get claimed", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const delivery = await createDelivery(tenantId, endpoint.id, { state: "in_flight" });
    const app = createApp();

    const res = await request(app).get(`/deliveries/${delivery.id}`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.body.blocked_on_delivery_id).toBeNull();
  });

  it("is never blocked once terminal (succeeded/failed), regardless of other endpoint traffic", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const succeeded = await createDelivery(tenantId, endpoint.id, { state: "succeeded" });
    await createDelivery(tenantId, endpoint.id, { state: "pending" }); // unrelated, still pending
    const app = createApp();

    const res = await request(app).get(`/deliveries/${succeeded.id}`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.body.blocked_on_delivery_id).toBeNull();
  });

  it("embeds the full attempts history and derives last_response from the most recent attempt", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const delivery = await createDelivery(tenantId, endpoint.id, { state: "pending" });
    await createAttempt(delivery.id, { attemptNumber: 1, responseStatus: 500, responseBodyTruncated: "server error", durationMs: 10 });
    await createAttempt(delivery.id, { attemptNumber: 2, responseStatus: null, errorClass: "total_timeout" });
    const app = createApp();

    const res = await request(app).get(`/deliveries/${delivery.id}`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.body.attempts).toHaveLength(2);
    expect(res.body.attempts.map((a: { attempt_number: number }) => a.attempt_number)).toEqual([1, 2]);
    expect(res.body.last_response).toMatchObject({ response_status: null, error_class: "total_timeout" });
  });

  it("caps the embedded attempts array at 6, keeping the most recent ones", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const delivery = await createDelivery(tenantId, endpoint.id, { state: "pending" });
    for (let attemptNumber = 1; attemptNumber <= 8; attemptNumber++) {
      await createAttempt(delivery.id, { attemptNumber, responseStatus: 500 });
    }
    const app = createApp();

    const res = await request(app).get(`/deliveries/${delivery.id}`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.body.attempts).toHaveLength(6);
    expect(res.body.attempts.map((a: { attempt_number: number }) => a.attempt_number)).toEqual([3, 4, 5, 6, 7, 8]);
  });

  it("returns 404 for a delivery belonging to a different tenant", async () => {
    const { id: otherTenantId } = await createTenant("other-tenant");
    const foreignEndpoint = await createEndpoint(otherTenantId, ["order.created"]);
    const foreignDelivery = await createDelivery(otherTenantId, foreignEndpoint.id);
    const { apiKey } = await createTenant();
    const app = createApp();

    const res = await request(app).get(`/deliveries/${foreignDelivery.id}`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.status).toBe(404);
  });

  it("returns 404 for a delivery that doesn't exist", async () => {
    const { apiKey } = await createTenant();
    const app = createApp();

    const res = await request(app)
      .get("/deliveries/00000000-0000-0000-0000-000000000000")
      .set("Authorization", `Bearer ${apiKey}`);

    expect(res.status).toBe(404);
  });
});
