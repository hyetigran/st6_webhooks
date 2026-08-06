import { describe, it, expect } from "vitest";
import request from "supertest";
import { createApp } from "../src/app.js";
import { pool } from "../src/db/pool.js";
import { createTenant, createEndpoint, createEvent } from "./fixtures.js";

async function linkDelivery(eventId: string, endpointId: string): Promise<void> {
  await pool.query("INSERT INTO deliveries (event_id, endpoint_id) VALUES ($1, $2)", [eventId, endpointId]);
}

describe("GET /events", () => {
  it("lists the tenant's events, newest first", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const older = await createEvent(tenantId, { createdAt: new Date(Date.now() - 60_000) });
    const newer = await createEvent(tenantId, { createdAt: new Date() });
    const app = createApp();

    const res = await request(app).get("/events").set("Authorization", `Bearer ${apiKey}`);

    expect(res.status).toBe(200);
    expect(res.body.events.map((e: { id: string }) => e.id)).toEqual([newer.id, older.id]);
  });

  it("never returns another tenant's events", async () => {
    const { apiKey } = await createTenant();
    const { id: otherTenantId } = await createTenant("other-tenant");
    await createEvent(otherTenantId);
    const app = createApp();

    const res = await request(app).get("/events").set("Authorization", `Bearer ${apiKey}`);

    expect(res.body.events).toEqual([]);
  });

  it("filters by type", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const shipped = await createEvent(tenantId, { type: "order.shipped" });
    await createEvent(tenantId, { type: "order.created" });
    const app = createApp();

    const res = await request(app).get("/events?type=order.shipped").set("Authorization", `Bearer ${apiKey}`);

    expect(res.body.events.map((e: { id: string }) => e.id)).toEqual([shipped.id]);
  });

  it("filters by id", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const target = await createEvent(tenantId);
    await createEvent(tenantId);
    const app = createApp();

    const res = await request(app).get(`/events?id=${target.id}`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.body.events.map((e: { id: string }) => e.id)).toEqual([target.id]);
  });

  it("filters by from/to time range", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const inRange = await createEvent(tenantId, { createdAt: new Date("2026-01-15T00:00:00.000Z") });
    await createEvent(tenantId, { createdAt: new Date("2026-02-15T00:00:00.000Z") }); // outside range
    const app = createApp();

    const res = await request(app)
      .get("/events?from=2026-01-01T00:00:00.000Z&to=2026-01-31T00:00:00.000Z")
      .set("Authorization", `Bearer ${apiKey}`);

    expect(res.body.events.map((e: { id: string }) => e.id)).toEqual([inRange.id]);
  });

  it("filters by endpoint_id, matching only events with a delivery to that endpoint", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpointA = await createEndpoint(tenantId, ["order.created"]);
    const endpointB = await createEndpoint(tenantId, ["order.created"]);
    const eventForA = await createEvent(tenantId);
    await linkDelivery(eventForA.id, endpointA.id);
    const eventForB = await createEvent(tenantId);
    await linkDelivery(eventForB.id, endpointB.id);
    const app = createApp();

    const res = await request(app).get(`/events?endpoint_id=${endpointA.id}`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.body.events.map((e: { id: string }) => e.id)).toEqual([eventForA.id]);
  });
});

describe("GET /events/:id", () => {
  it("returns the event with its fan-out deliveries", async () => {
    const { apiKey, id: tenantId } = await createTenant();
    const endpointA = await createEndpoint(tenantId, ["order.created"]);
    const endpointB = await createEndpoint(tenantId, ["order.created"]);
    const event = await createEvent(tenantId, { type: "order.created", payload: { orderId: "abc" } });
    await linkDelivery(event.id, endpointA.id);
    await linkDelivery(event.id, endpointB.id);
    const app = createApp();

    const res = await request(app).get(`/events/${event.id}`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.status).toBe(200);
    expect(res.body).toMatchObject({ id: event.id, type: "order.created", payload: { orderId: "abc" } });
    expect(res.body.deliveries).toHaveLength(2);
    const endpointIds = res.body.deliveries.map((d: { endpoint_id: string }) => d.endpoint_id).sort();
    expect(endpointIds).toEqual([endpointA.id, endpointB.id].sort());
  });

  it("returns 404 for an event belonging to a different tenant", async () => {
    const { id: otherTenantId } = await createTenant("other-tenant");
    const foreignEvent = await createEvent(otherTenantId);
    const { apiKey } = await createTenant();
    const app = createApp();

    const res = await request(app).get(`/events/${foreignEvent.id}`).set("Authorization", `Bearer ${apiKey}`);

    expect(res.status).toBe(404);
  });
});
