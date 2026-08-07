import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
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

export function EventDetail() {
  const { id } = useParams<{ id: string }>();
  const client = useApiClient();
  const { backend } = useBackend();
  const navigate = useNavigate();

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ["event", backend.id, id],
    queryFn: () => client!.getEvent(id!),
    enabled: client !== null && !!id,
    refetchInterval: 3000,
  });

  if (!client) return null;

  return (
    <div>
      <p style={{ fontSize: 13, color: "var(--color-text-muted)", marginBottom: 8 }}>
        <Link to="/console/events">Events</Link> / <span className="app-mono">{id}</span>
      </p>

      {isLoading && <p>Loading…</p>}
      {isError && <p style={{ color: "var(--color-danger)" }}>Failed to load event: {(error as Error).message}</p>}

      {data && (
        <>
          <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 20 }}>
            <h1 style={{ fontSize: 28 }}>{data.type}</h1>
            <Badge tone={data.status === "expanded" ? "accent" : "neutral"}>{data.status}</Badge>
          </div>

          <div style={{ display: "grid", gridTemplateColumns: "repeat(3, 1fr)", gap: 16, marginBottom: 20 }}>
            <Field label="Event ID" value={data.id} mono />
            <Field label="Published" value={formatDateTime(data.created_at)} />
            <Field label="Status" value={data.status} />
          </div>

          <Card style={{ marginBottom: 20 }}>
            <h3 style={{ fontSize: 13, marginBottom: 8, color: "var(--color-text-muted)" }}>PAYLOAD</h3>
            <pre
              style={{
                margin: 0,
                fontFamily: "var(--font-mono)",
                fontSize: 12,
                whiteSpace: "pre-wrap",
                wordBreak: "break-word",
              }}
            >
              {JSON.stringify(data.payload, null, 2)}
            </pre>
          </Card>

          <h2 style={{ fontSize: 18, marginBottom: 12 }}>Fan-out across endpoints</h2>
          {data.deliveries.length === 0 ? (
            <Card>
              <p style={{ margin: 0, color: "var(--color-text-muted)" }}>
                Not expanded yet — deliveries will appear here once the worker fans this event out to subscribed endpoints.
              </p>
            </Card>
          ) : (
            <Card cornerMarks style={{ padding: 0 }}>
              <table className="app-table">
                <thead>
                  <tr>
                    <th>Delivery</th>
                    <th>Endpoint</th>
                    <th>State</th>
                    <th>Attempts</th>
                    <th>Next attempt</th>
                  </tr>
                </thead>
                <tbody>
                  {data.deliveries.map((d) => (
                    <tr key={d.id} className="clickable" onClick={() => navigate(`/console/deliveries/${d.id}`)}>
                      <td className="app-mono">{d.id}</td>
                      <td className="app-mono">{d.endpoint_id}</td>
                      <td>
                        <Badge tone={deliveryTone(d.state)}>{d.state}</Badge>
                      </td>
                      <td>{d.attempt_count}</td>
                      <td>{d.state === "pending" || d.state === "in_flight" ? formatRelativeTime(d.next_attempt_at) : "—"}</td>
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
