import { describe, it, expect } from "vitest";
import request from "supertest";
import { createApp } from "../src/app.js";
import { createTenant, createEndpoint, createDelivery } from "./fixtures.js";

describe("GET /endpoints/:id/deliveries", () => {
  it("returns the endpoint's deliveries ordered head-first (ascending seq), with the head unblocked and the rest blocked on it", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const head = await createDelivery(tenantId, endpoint.id, { state: "pending" });
    const second = await createDelivery(tenantId, endpoint.id, { state: "pending" });
    const app = createApp();

    const res = await request(app).get(`/endpoints/${endpoint.id}/deliveries`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.status).toBe(200);
    expect(res.body.deliveries.map((d: { id: string }) => d.id)).toEqual([head.id, second.id]);
    expect(res.body.deliveries[0].blocked_on_delivery_id).toBeNull();
    expect(res.body.deliveries[1].blocked_on_delivery_id).toBe(head.id);
  });

  it("only returns deliveries for this endpoint, not others", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpointA = await createEndpoint(tenantId, ["order.created"]);
    const endpointB = await createEndpoint(tenantId, ["order.created"]);
    const forA = await createDelivery(tenantId, endpointA.id);
    await createDelivery(tenantId, endpointB.id);
    const app = createApp();

    const res = await request(app).get(`/endpoints/${endpointA.id}/deliveries`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.body.deliveries.map((d: { id: string }) => d.id)).toEqual([forA.id]);
  });

  it("paginates with a seq-based cursor", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpoint = await createEndpoint(tenantId, ["order.created"]);
    const first = await createDelivery(tenantId, endpoint.id);
    const second = await createDelivery(tenantId, endpoint.id);
    const app = createApp();

    const firstPage = await request(app)
      .get(`/endpoints/${endpoint.id}/deliveries?limit=1`)
      .set("Authorization", `Bearer ${apiKey}`);

    expect(firstPage.body.deliveries.map((d: { id: string }) => d.id)).toEqual([first.id]);
    expect(firstPage.body.next_cursor).not.toBeNull();

    const secondPage = await request(app)
      .get(`/endpoints/${endpoint.id}/deliveries?limit=1&after=${firstPage.body.next_cursor}`)
      .set("Authorization", `Bearer ${apiKey}`);

    expect(secondPage.body.deliveries.map((d: { id: string }) => d.id)).toEqual([second.id]);
    expect(secondPage.body.next_cursor).toBeNull();
  });

  it("returns 404 for an endpoint belonging to a different tenant", async () => {
    const { id: otherTenantId } = await createTenant("other-tenant");
    const foreignEndpoint = await createEndpoint(otherTenantId, ["order.created"]);
    const { apiKey } = await createTenant();
    const app = createApp();

    const res = await request(app).get(`/endpoints/${foreignEndpoint.id}/deliveries`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.status).toBe(404);
  });
});
