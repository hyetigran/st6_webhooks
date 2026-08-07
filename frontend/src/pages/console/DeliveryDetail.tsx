import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { useApiClient } from "../../api/useApiClient";
import { useBackend } from "../../lib/backend";
import { Badge } from "../../design/Badge";
import { Card } from "../../design/Card";
import "../../design/Table.css";
import { formatDateTime, formatRelativeTime } from "../../lib/format";
import type { DeliveryState } from "../../api/types";

function deliveryTone(state: DeliveryState): "neutral" | "accent" | "danger" {
  if (state === "failed") return "danger";
  if (state === "in_flight" || state === "succeeded") return "accent";
  return "neutral";
}

function outstandingReason(state: DeliveryState, blockedOnDeliveryId: string | null): string | null {
  if (state === "succeeded") return null;
  if (state === "failed") return "Reached the attempt ceiling — this endpoint is now halted.";
  if (state === "in_flight") return "A worker holds the claim and the request is outstanding right now.";
  if (blockedOnDeliveryId) return "Blocked: waiting for an earlier delivery on this endpoint's queue to resolve first.";
  return "Pending — scheduled for its next attempt.";
}

export function DeliveryDetail() {
  const { id } = useParams<{ id: string }>();
  const client = useApiClient();
  const { backend } = useBackend();

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ["delivery", backend.id, id],
    queryFn: () => client!.getDelivery(id!),
    enabled: client !== null && !!id,
    refetchInterval: 3000,
  });

  if (!client) return null;

  return (
    <div>
      {isLoading && <p>Loading…</p>}
      {isError && <p style={{ color: "var(--color-danger)" }}>Failed to load delivery: {(error as Error).message}</p>}

      {data && (
        <>
          <p style={{ fontSize: 13, color: "var(--color-text-muted)", marginBottom: 8 }}>
            <Link to={`/console/events/${data.event_id}`}>Event {data.event_id}</Link> /{" "}
            <Link to={`/console/endpoints/${data.endpoint_id}`}>Endpoint queue</Link>
          </p>

          <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 8 }}>
            <h1 style={{ fontSize: 24 }} className="app-mono">
              {data.id}
            </h1>
            <Badge tone={deliveryTone(data.state)}>{data.state}</Badge>
          </div>

          <p style={{ fontSize: 14, marginBottom: 20 }}>{outstandingReason(data.state, data.blocked_on_delivery_id)}</p>

          <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 16, marginBottom: 20 }}>
            <Field label="Event ID" value={data.event_id} mono />
            <Field label="Endpoint ID" value={data.endpoint_id} mono />
            <Field label="Attempts" value={`${data.attempt_count} of 6`} />
            <Field
              label="Next attempt"
              value={data.state === "pending" || data.state === "in_flight" ? formatRelativeTime(data.next_attempt_at) : "—"}
            />
          </div>

          {data.blocked_on_delivery_id && (
            <p style={{ fontSize: 13, marginBottom: 20 }}>
              Blocked on{" "}
              <Link to={`/console/deliveries/${data.blocked_on_delivery_id}`} className="app-mono">
                {data.blocked_on_delivery_id}
              </Link>
            </p>
          )}

          {data.last_response && (
            <Card style={{ marginBottom: 20 }}>
              <h3 style={{ fontSize: 13, marginBottom: 12, color: "var(--color-text-muted)" }}>LAST RESPONSE</h3>
              <div style={{ display: "grid", gridTemplateColumns: "repeat(3, 1fr)", gap: 16 }}>
                <Field label="Status" value={data.last_response.response_status?.toString() ?? "—"} />
                <Field label="Duration" value={data.last_response.duration_ms !== null ? `${data.last_response.duration_ms}ms` : "—"} />
                <Field label="Error class" value={data.last_response.error_class ?? "—"} />
              </div>
              {data.last_response.response_body_truncated && (
                <pre
                  style={{
                    marginTop: 12,
                    fontFamily: "var(--font-mono)",
                    fontSize: 12,
                    whiteSpace: "pre-wrap",
                    wordBreak: "break-word",
                    background: "var(--color-badge-bg)",
                    padding: 10,
                  }}
                >
                  {data.last_response.response_body_truncated}
                </pre>
              )}
            </Card>
          )}

          <h2 style={{ fontSize: 18, marginBottom: 12 }}>Attempt timeline</h2>
          {data.attempts.length === 0 ? (
            <Card>
              <p style={{ margin: 0, color: "var(--color-text-muted)" }}>No attempts yet.</p>
            </Card>
          ) : (
            <Card cornerMarks style={{ padding: 0 }}>
              <table className="app-table">
                <thead>
                  <tr>
                    <th>#</th>
                    <th>Sent</th>
                    <th>Status</th>
                    <th>Duration</th>
                    <th>Error</th>
                  </tr>
                </thead>
                <tbody>
                  {data.attempts.map((a) => (
                    <tr key={a.attempt_number}>
                      <td>{a.attempt_number}</td>
                      <td>{a.sent_at ? formatDateTime(a.sent_at) : "outstanding"}</td>
                      <td>{a.response_status ?? "—"}</td>
                      <td>{a.duration_ms !== null ? `${a.duration_ms}ms` : "—"}</td>
                      <td>{a.error_class ?? "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </Card>
          )}
        </>
      )}
    </div>
  );
}

function Field({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <div style={{ fontSize: 11, letterSpacing: 0.4, textTransform: "uppercase", color: "var(--color-text-muted)" }}>
        {label}
      </div>
      <div className={mono ? "app-mono" : undefined} style={{ fontSize: 14 }}>
        {value}
      </div>
    </div>
  );
}
