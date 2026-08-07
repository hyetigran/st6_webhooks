export interface Endpoint {
  id: string;
  url: string;
  event_types: string[];
  status: "active" | "paused" | "halted";
  created_at: string;
}

export interface EndpointHealth {
  queue_depth: number;
  oldest_pending_at: string | null;
  recent_success_rate: number | null;
}

export type EndpointWithHealth = Endpoint & EndpointHealth;

export type RegisterEndpointResponse = Endpoint & { signing_secret: string };

export type ResumeEndpointResponse = Endpoint & {
  pending_delivery_count: number;
  skipped_failed_delivery_ids: string[];
};

export interface RotateSecretResponse {
  signing_secret: string;
  overlap_expires_at: string;
}

export type EndpointListResponse = { endpoints: EndpointWithHealth[]; next_cursor: string | null };

export interface Event {
  id: string;
  type: string;
  payload: unknown;
  status: "pending_expansion" | "expanded";
  created_at: string;
}

export interface DeliveryFanoutSummary {
  id: string;
  endpoint_id: string;
  state: DeliveryState;
  attempt_count: number;
  next_attempt_at: string;
}

export type EventDetail = Event & { deliveries: DeliveryFanoutSummary[] };

export type EventListResponse = { events: Event[]; next_cursor: string | null };

export type DeliveryState = "pending" | "in_flight" | "succeeded" | "failed";

export interface DeliverySummary {
  id: string;
  event_id: string;
  state: DeliveryState;
  attempt_count: number;
  next_attempt_at: string;
  blocked_on_delivery_id: string | null;
}

export interface Attempt {
  attempt_number: number;
  sent_at: string | null;
  response_status: number | null;
  response_body_truncated: string | null;
  duration_ms: number | null;
  error_class: string | null;
}

export type LastResponse = Pick<
  Attempt,
  "response_status" | "response_body_truncated" | "duration_ms" | "error_class"
>;

export type DeliveryDetail = DeliverySummary & {
  endpoint_id: string;
  last_response: LastResponse | null;
  attempts: Attempt[];
};

export type EndpointDeliveriesResponse = { deliveries: DeliverySummary[]; next_cursor: string | null };

export interface AsyncAcceptedResponse {
  id: string;
  status: string;
}

export interface RegisterEndpointInput {
  url: string;
  event_types: string[];
}

export interface PublishEventInput {
  type: string;
  payload: Record<string, unknown>;
}

export interface TriggerReplayInput {
  range_start: string;
  range_end: string;
}

export interface CursorPageParams {
  limit?: number;
  cursor?: string | null;
}

export interface EventSearchParams extends CursorPageParams {
  id?: string;
  type?: string;
  endpoint_id?: string;
  from?: string;
  to?: string;
}

/** Structured error body every route returns on a non-2xx response
 * (Shared REST API contract). */
export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
  };
}
