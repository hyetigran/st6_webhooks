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

type RequestInit = { method: string; body?: unknown; headers?: Record<string, string> };

async function request<T>(fetchFn: typeof fetch, baseUrl: string, apiKey: string, path: string, init: RequestInit): Promise<T> {
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
function pageQueryParams(params: CursorPageParams | undefined, cursorParam: "before" | "after" = "before"): URLSearchParams {
  const q = new URLSearchParams();
  if (params?.limit !== undefined) q.set("limit", String(params.limit));
  if (params?.cursor) q.set(cursorParam, params.cursor);
  return q;
}

function toQueryString(q: URLSearchParams): string {
  const qs = q.toString();
  return qs ? `?${qs}` : "";
}

function eventSearchQuery(params: EventSearchParams | undefined): string {
  const q = pageQueryParams(params);
  if (params?.id) q.set("id", params.id);
  if (params?.type) q.set("type", params.type);
  if (params?.endpoint_id) q.set("endpoint_id", params.endpoint_id);
  if (params?.from) q.set("from", params.from);
  if (params?.to) q.set("to", params.to);
  return toQueryString(q);
}

export function createApiClient({ baseUrl, apiKey, fetchFn = fetch }: ApiClientOptions) {
  const call = <T>(path: string, init: RequestInit): Promise<T> => request<T>(fetchFn, baseUrl, apiKey, path, init);

  return {
    registerEndpoint(input: RegisterEndpointInput): Promise<RegisterEndpointResponse> {
      return call("/endpoints", { method: "POST", body: input });
    },

    listEndpoints(params?: CursorPageParams): Promise<EndpointListResponse> {
      return call(`/endpoints${toQueryString(pageQueryParams(params))}`, { method: "GET" });
    },

    publishEvent(input: PublishEventInput, idempotencyKey: string): Promise<AsyncAcceptedResponse> {
      return call("/events", { method: "POST", body: input, headers: { "idempotency-key": idempotencyKey } });
    },

    getEndpoint(id: string): Promise<EndpointWithHealth> {
      return call(`/endpoints/${id}`, { method: "GET" });
    },

    pauseEndpoint(id: string): Promise<Endpoint> {
      return call(`/endpoints/${id}/pause`, { method: "POST" });
    },

    resumeEndpoint(id: string): Promise<ResumeEndpointResponse> {
      return call(`/endpoints/${id}/resume`, { method: "POST" });
    },

    rotateSecret(id: string): Promise<RotateSecretResponse> {
      return call(`/endpoints/${id}/secret/rotate`, { method: "POST" });
    },

    listEvents(params?: EventSearchParams): Promise<EventListResponse> {
      return call(`/events${eventSearchQuery(params)}`, { method: "GET" });
    },

    getEvent(id: string): Promise<EventDetail> {
      return call(`/events/${id}`, { method: "GET" });
    },

    getDelivery(id: string): Promise<DeliveryDetail> {
      return call(`/deliveries/${id}`, { method: "GET" });
    },

    listEndpointDeliveries(endpointId: string, params?: CursorPageParams): Promise<EndpointDeliveriesResponse> {
      return call(`/endpoints/${endpointId}/deliveries${toQueryString(pageQueryParams(params, "after"))}`, { method: "GET" });
    },

    triggerReplay(endpointId: string, input: TriggerReplayInput, idempotencyKey: string): Promise<AsyncAcceptedResponse> {
      return call(`/endpoints/${endpointId}/replays`, { method: "POST", body: input, headers: { "idempotency-key": idempotencyKey } });
    },
  };
}
