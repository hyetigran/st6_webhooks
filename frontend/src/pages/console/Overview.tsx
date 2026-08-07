import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { useApiClient } from "../../api/useApiClient";
import { useBackend } from "../../lib/backend";
import { Badge } from "../../design/Badge";
import { Card } from "../../design/Card";
import { StatCard } from "../../design/StatCard";
import "../../design/Table.css";
import { formatDateTime, formatRelativeTime } from "../../lib/format";

const ONE_HOUR_MS = 60 * 60 * 1000;

/** Home page for the console — not one of PRD §7's 4 required surfaces,
 * built as agreed bonus scope matching the reference mockup. Every stat
 * here is a real aggregate over data the REST API actually returns (no
 * dedicated "dashboard summary" route exists) — queue_depth/
 * recent_success_rate summed/averaged across GET /endpoints, "events
 * published" counted from GET /events?from=. There's no tenant-wide
 * "recent deliveries" endpoint (only per-endpoint or per-event), so this
 * shows recent *events* instead of inventing an N+1 fan-out fetch for a
 * summary page. */
export function Overview() {
  const client = useApiClient();
  const { backend } = useBackend();

  const endpointsQuery = useQuery({
    queryKey: ["endpoints", backend.id],
    queryFn: () => client!.listEndpoints({ limit: 100 }),
    enabled: client !== null,
    refetchInterval: 3000,
  });

  const eventsQuery = useQuery({
    queryKey: ["events", backend.id, "recent"],
    queryFn: () => client!.listEvents({ limit: 10 }),
    enabled: client !== null,
    refetchInterval: 3000,
  });

  const recentEventsQuery = useQuery({
    queryKey: ["events", backend.id, "since-1h"],
    queryFn: () => client!.listEvents({ limit: 100, from: new Date(Date.now() - ONE_HOUR_MS).toISOString() }),
    enabled: client !== null,
    refetchInterval: 3000,
  });

  if (!client) return null;

  const endpoints = endpointsQuery.data?.endpoints ?? [];
  const pendingTotal = endpoints.reduce((sum, e) => sum + e.queue_depth, 0);
  const withSuccessRate = endpoints.filter((e) => e.recent_success_rate !== null);
  const avgSuccessRate =
    withSuccessRate.length === 0
      ? null
      : withSuccessRate.reduce((sum, e) => sum + e.recent_success_rate!, 0) / withSuccessRate.length;
  const needingAttention = endpoints.filter((e) => e.status === "halted" || e.status === "paused");

  return (
    <div>
      <h1 style={{ fontSize: 28, marginBottom: 4 }}>Delivery health</h1>
      <p style={{ fontSize: 13, color: "var(--color-text-muted)", marginBottom: 20 }}>Polling every 3s · {backend.label}</p>

      <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 16, marginBottom: 24 }}>
        <StatCard label="Events published (1h)" value={recentEventsQuery.data ? recentEventsQuery.data.events.length : "—"} />
        <StatCard label="Deliveries pending" value={endpointsQuery.data ? pendingTotal : "—"} />
        <StatCard
          label="Success rate (avg)"
          value={avgSuccessRate === null ? "—" : `${Math.round(avgSuccessRate * 100)}%`}
        />
        <StatCard label="Endpoints needing you" value={endpointsQuery.data ? needingAttention.length : "—"} />
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "1fr 2fr", gap: 24 }}>
        <div>
          <h2 style={{ fontSize: 18, marginBottom: 12 }}>Needs attention</h2>
          {needingAttention.length === 0 ? (
            <Card>
              <p style={{ margin: 0, color: "var(--color-text-muted)", fontSize: 13 }}>
                Every endpoint is active — nothing needs a look right now.
              </p>
            </Card>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
              {needingAttention.map((e) => (
                <Card key={e.id}>
                  <Badge tone={e.status === "halted" ? "danger" : "neutral"}>{e.status}</Badge>
                  <Link to={`/console/endpoints/${e.id}`} style={{ display: "block", fontWeight: 600, marginTop: 8 }}>
                    {e.url}
                  </Link>
                  <p style={{ fontSize: 12, color: "var(--color-text-muted)", margin: "4px 0 0" }}>
                    {e.queue_depth} pending · oldest {formatRelativeTime(e.oldest_pending_at)}
                  </p>
                </Card>
              ))}
            </div>
          )}
        </div>

        <div>
          <h2 style={{ fontSize: 18, marginBottom: 12 }}>Recent events</h2>
          {eventsQuery.data && eventsQuery.data.events.length === 0 && (
            <Card>
              <p style={{ margin: 0, color: "var(--color-text-muted)", fontSize: 13 }}>Nothing published yet.</p>
            </Card>
          )}
          {eventsQuery.data && eventsQuery.data.events.length > 0 && (
            <Card cornerMarks style={{ padding: 0 }}>
              <table className="app-table">
                <thead>
                  <tr>
                    <th>Event</th>
                    <th>Type</th>
                    <th>Published</th>
                    <th>Status</th>
                  </tr>
                </thead>
                <tbody>
                  {eventsQuery.data.events.map((event) => (
                    <tr key={event.id}>
                      <td className="app-mono">
                        <Link to={`/console/events/${event.id}`} className="app-mono">
                          {event.id}
                        </Link>
                      </td>
                      <td>{event.type}</td>
                      <td>{formatDateTime(event.created_at)}</td>
                      <td>{event.status}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </Card>
          )}
        </div>
      </div>
    </div>
  );
}
