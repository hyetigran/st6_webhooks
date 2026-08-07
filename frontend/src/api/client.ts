import type {
  ApiErrorBody,
  AsyncAcceptedResponse,
  CursorPageParams,
  DeliveryDetail,
  Endpoint,
  EndpointDeliveriesResponse,
  EndpointListResponse,
  EndpointWithHealth,
  EventDetail,
  EventListResponse,
  EventSearchParams,
  PublishEventInput,
  RegisterEndpointInput,
  RegisterEndpointResponse,
  ResumeEndpointResponse,
  RotateSecretResponse,
  TriggerReplayInput,
} from "./types";

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(code: string, message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
  }
}

export interface ApiClientOptions {
  baseUrl: string;
  apiKey: string;
  /** Injected for testability — defaults to the global fetch. */
  fetchFn?: typeof fetch;
}

async function request<T>(
  fetchFn: typeof fetch,
  baseUrl: string,
  apiKey: string,
  path: string,
  init: { method: string; body?: unknown; headers?: Record<string, string> },
): Promise<T> {
  const headers: Record<string, string> = {
    authorization: `Bearer ${apiKey}`,
    ...init.headers,
  };
  if (init.body !== undefined) {
    headers["content-type"] = "application/json";
  }

  const res = await fetchFn(`${baseUrl}${path}`, {
    method: init.method,
    headers,
    body: init.body !== undefined ? JSON.stringify(init.body) : undefined,
  });

  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as ApiErrorBody | null;
    throw new ApiError(body?.error.code ?? "unknown_error", body?.error.message ?? res.statusText, res.status);
  }

  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

/** `before` is this API's cursor query-param name on every list route
 * except the endpoint-queue view, which uses `after` (docs/adr/0007 —
 * ascending seq order, not newest-first). */
function pageQuery(params: CursorPageParams | undefined, cursorParam: "before" | "after" = "before"): string {
  const q = new URLSearchParams();
  if (params?.limit !== undefined) q.set("limit", String(params.limit));
  if (params?.cursor) q.set(cursorParam, params.cursor);
  const qs = q.toString();
  return qs ? `?${qs}` : "";
}

function eventSearchQuery(params: EventSearchParams | undefined): string {
  const q = new URLSearchParams();
  if (params?.limit !== undefined) q.set("limit", String(params.limit));
  if (params?.cursor) q.set("before", params.cursor);
  if (params?.id) q.set("id", params.id);
  if (params?.type) q.set("type", params.type);
  if (params?.endpoint_id) q.set("endpoint_id", params.endpoint_id);
  if (params?.from) q.set("from", params.from);
  if (params?.to) q.set("to", params.to);
  const qs = q.toString();
  return qs ? `?${qs}` : "";
}

export function createApiClient({ baseUrl, apiKey, fetchFn = fetch }: ApiClientOptions) {
  return {
    registerEndpoint(input: RegisterEndpointInput): Promise<RegisterEndpointResponse> {
      return request<RegisterEndpointResponse>(fetchFn, baseUrl, apiKey, "/endpoints", { method: "POST", body: input });
    },

    listEndpoints(params?: CursorPageParams): Promise<EndpointListResponse> {
      return request<EndpointListResponse>(fetchFn, baseUrl, apiKey, `/endpoints${pageQuery(params)}`, { method: "GET" });
    },

    publishEvent(input: PublishEventInput, idempotencyKey: string): Promise<AsyncAcceptedResponse> {
      return request<AsyncAcceptedResponse>(fetchFn, baseUrl, apiKey, "/events", {
        method: "POST",
        body: input,
        headers: { "idempotency-key": idempotencyKey },
      });
    },

    getEndpoint(id: string): Promise<EndpointWithHealth> {
      return request<EndpointWithHealth>(fetchFn, baseUrl, apiKey, `/endpoints/${id}`, { method: "GET" });
    },

    pauseEndpoint(id: string): Promise<Endpoint> {
      return request<Endpoint>(fetchFn, baseUrl, apiKey, `/endpoints/${id}/pause`, { method: "POST" });
    },

    resumeEndpoint(id: string): Promise<ResumeEndpointResponse> {
      return request<ResumeEndpointResponse>(fetchFn, baseUrl, apiKey, `/endpoints/${id}/resume`, { method: "POST" });
    },

    rotateSecret(id: string): Promise<RotateSecretResponse> {
      return request<RotateSecretResponse>(fetchFn, baseUrl, apiKey, `/endpoints/${id}/secret/rotate`, { method: "POST" });
    },

    listEvents(params?: EventSearchParams): Promise<EventListResponse> {
      return request<EventListResponse>(fetchFn, baseUrl, apiKey, `/events${eventSearchQuery(params)}`, { method: "GET" });
    },

    getEvent(id: string): Promise<EventDetail> {
      return request<EventDetail>(fetchFn, baseUrl, apiKey, `/events/${id}`, { method: "GET" });
    },

    getDelivery(id: string): Promise<DeliveryDetail> {
      return request<DeliveryDetail>(fetchFn, baseUrl, apiKey, `/deliveries/${id}`, { method: "GET" });
    },

    listEndpointDeliveries(endpointId: string, params?: CursorPageParams): Promise<EndpointDeliveriesResponse> {
      return request<EndpointDeliveriesResponse>(
        fetchFn,
        baseUrl,
        apiKey,
        `/endpoints/${endpointId}/deliveries${pageQuery(params, "after")}`,
        { method: "GET" },
      );
    },

    triggerReplay(endpointId: string, input: TriggerReplayInput, idempotencyKey: string): Promise<AsyncAcceptedResponse> {
      return request<AsyncAcceptedResponse>(fetchFn, baseUrl, apiKey, `/endpoints/${endpointId}/replays`, {
        method: "POST",
        body: input,
        headers: { "idempotency-key": idempotencyKey },
      });
    },
  };
}
