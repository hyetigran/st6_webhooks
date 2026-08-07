import { describe, expect, it, vi } from "vitest";
import { ApiError, createApiClient } from "./client";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

describe("createApiClient", () => {
  it("registers an endpoint with Bearer auth and returns the parsed body", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(201, {
        id: "ep_1",
        url: "https://example.com/hook",
        event_types: ["order.created"],
        status: "active",
        created_at: "2026-01-01T00:00:00.000Z",
        signing_secret: "whsec_abc123",
      }),
    );
    const client = createApiClient({ baseUrl: "http://localhost:3000", apiKey: "tenant_xyz", fetchFn: fetchMock });

    const result = await client.registerEndpoint({ url: "https://example.com/hook", event_types: ["order.created"] });

    expect(result).toEqual({
      id: "ep_1",
      url: "https://example.com/hook",
      event_types: ["order.created"],
      status: "active",
      created_at: "2026-01-01T00:00:00.000Z",
      signing_secret: "whsec_abc123",
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:3000/endpoints");
    expect(init.method).toBe("POST");
    const headers = new Headers(init.headers);
    expect(headers.get("authorization")).toBe("Bearer tenant_xyz");
    expect(headers.get("content-type")).toBe("application/json");
    expect(JSON.parse(init.body as string)).toEqual({ url: "https://example.com/hook", event_types: ["order.created"] });
  });

  it("throws an ApiError carrying the structured error code/message on a non-2xx response", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(400, { error: { code: "url_not_allowed", message: "URL not allowed" } }),
    );
    const client = createApiClient({ baseUrl: "http://localhost:3000", apiKey: "tenant_xyz", fetchFn: fetchMock });

    await expect(client.registerEndpoint({ url: "http://localhost/hook", event_types: ["a"] })).rejects.toMatchObject({
      code: "url_not_allowed",
      message: "URL not allowed",
      status: 400,
    });
  });

  it("throws an ApiError instance specifically, not a generic Error", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(404, { error: { code: "not_found", message: "Endpoint not found" } }));
    const client = createApiClient({ baseUrl: "http://localhost:3000", apiKey: "tenant_xyz", fetchFn: fetchMock });

    await expect(client.registerEndpoint({ url: "https://example.com", event_types: ["a"] })).rejects.toBeInstanceOf(ApiError);
  });

  it("lists endpoints with no query params when none are given", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { endpoints: [], next_cursor: null }));
    const client = createApiClient({ baseUrl: "http://localhost:3000", apiKey: "tenant_xyz", fetchFn: fetchMock });

    const result = await client.listEndpoints();

    expect(result).toEqual({ endpoints: [], next_cursor: null });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:3000/endpoints");
    expect(init.method).toBe("GET");
  });

  it("lists endpoints with limit and before-cursor query params, and parses a health-shaped page", async () => {
    const page = {
      endpoints: [
        {
          id: "ep_1",
          url: "https://example.com/hook",
          event_types: ["order.created"],
          status: "active",
          created_at: "2026-01-01T00:00:00.000Z",
          queue_depth: 2,
          oldest_pending_at: "2026-01-01T00:01:00.000Z",
          recent_success_rate: 0.5,
        },
      ],
      next_cursor: "opaque-cursor",
    };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, page));
    const client = createApiClient({ baseUrl: "http://localhost:3000", apiKey: "tenant_xyz", fetchFn: fetchMock });

    const result = await client.listEndpoints({ limit: 10, cursor: "prev-cursor" });

    expect(result).toEqual(page);
    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:3000/endpoints?limit=10&before=prev-cursor");
  });

  it("publishes an event with the Idempotency-Key header (not a body field) and returns 202's body", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { id: "evt_1", status: "pending_expansion" }));
    const client = createApiClient({ baseUrl: "http://localhost:3000", apiKey: "tenant_xyz", fetchFn: fetchMock });

    const result = await client.publishEvent({ type: "order.created", payload: { orderId: "abc" } }, "order-123");

    expect(result).toEqual({ id: "evt_1", status: "pending_expansion" });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:3000/events");
    expect(init.method).toBe("POST");
    const headers = new Headers(init.headers);
    expect(headers.get("idempotency-key")).toBe("order-123");
    expect(JSON.parse(init.body as string)).toEqual({ type: "order.created", payload: { orderId: "abc" } });
  });

  it("fetches a delivery's full detail: last_response, attempts array, blocked_on_delivery_id", async () => {
    const detail = {
      id: "dlv_1",
      event_id: "evt_1",
      endpoint_id: "ep_1",
      state: "failed",
      attempt_count: 2,
      next_attempt_at: "2026-01-01T00:05:00.000Z",
      blocked_on_delivery_id: null,
      last_response: { response_status: 500, response_body_truncated: "oops", duration_ms: 120, error_class: null },
      attempts: [
        { attempt_number: 1, sent_at: "2026-01-01T00:00:00.000Z", response_status: 500, response_body_truncated: "oops", duration_ms: 100, error_class: null },
        { attempt_number: 2, sent_at: "2026-01-01T00:02:00.000Z", response_status: 500, response_body_truncated: "oops", duration_ms: 120, error_class: null },
      ],
    };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, detail));
    const client = createApiClient({ baseUrl: "http://localhost:3000", apiKey: "tenant_xyz", fetchFn: fetchMock });

    const result = await client.getDelivery("dlv_1");

    expect(result).toEqual(detail);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:3000/deliveries/dlv_1");
    expect(init.method).toBe("GET");
  });

  it("resumes an endpoint, returning the R-14 ordering-consequence disclosure regardless of prior status", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        id: "ep_1",
        url: "https://example.com/hook",
        event_types: ["order.created"],
        status: "active",
        created_at: "2026-01-01T00:00:00.000Z",
        pending_delivery_count: 3,
        skipped_failed_delivery_ids: ["dlv_a", "dlv_b"],
      }),
    );
    const client = createApiClient({ baseUrl: "http://localhost:3000", apiKey: "tenant_xyz", fetchFn: fetchMock });

    const result = await client.resumeEndpoint("ep_1");

    expect(result.pending_delivery_count).toBe(3);
    expect(result.skipped_failed_delivery_ids).toEqual(["dlv_a", "dlv_b"]);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:3000/endpoints/ep_1/resume");
    expect(init.method).toBe("POST");
  });

  it("lists an endpoint's queue using an after-cursor, not a before-cursor (docs/adr/0007)", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { deliveries: [], next_cursor: null }));
    const client = createApiClient({ baseUrl: "http://localhost:3000", apiKey: "tenant_xyz", fetchFn: fetchMock });

    await client.listEndpointDeliveries("ep_1", { limit: 5, cursor: "seq-cursor" });

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:3000/endpoints/ep_1/deliveries?limit=5&after=seq-cursor");
  });

  it("triggers a replay with a time range body and the Idempotency-Key header", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { id: "rep_1", status: "pending_expansion" }));
    const client = createApiClient({ baseUrl: "http://localhost:3000", apiKey: "tenant_xyz", fetchFn: fetchMock });

    const result = await client.triggerReplay(
      "ep_1",
      { range_start: "2026-01-01T00:00:00.000Z", range_end: "2026-01-02T00:00:00.000Z" },
      "replay-key-1",
    );

    expect(result).toEqual({ id: "rep_1", status: "pending_expansion" });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:3000/endpoints/ep_1/replays");
    expect(init.method).toBe("POST");
    const headers = new Headers(init.headers);
    expect(headers.get("idempotency-key")).toBe("replay-key-1");
    expect(JSON.parse(init.body as string)).toEqual({
      range_start: "2026-01-01T00:00:00.000Z",
      range_end: "2026-01-02T00:00:00.000Z",
    });
  });

  it("rotates an endpoint's signing secret, returning the new secret and its overlap-expiry (R-3)", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, { signing_secret: "whsec_new", overlap_expires_at: "2026-01-02T00:00:00.000Z" }),
    );
    const client = createApiClient({ baseUrl: "http://localhost:3000", apiKey: "tenant_xyz", fetchFn: fetchMock });

    const result = await client.rotateSecret("ep_1");

    expect(result).toEqual({ signing_secret: "whsec_new", overlap_expires_at: "2026-01-02T00:00:00.000Z" });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:3000/endpoints/ep_1/secret/rotate");
    expect(init.method).toBe("POST");
  });
});
