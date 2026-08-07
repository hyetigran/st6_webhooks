import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { useApiClient } from "../../api/useApiClient";
import { useBackend } from "../../lib/backend";
import { Badge } from "../../design/Badge";
import { Card } from "../../design/Card";
import { ErrorState } from "../../design/ErrorState";
import { LoadingState } from "../../design/LoadingState";
import { StatCard } from "../../design/StatCard";
import "../../design/Table.css";
import { formatDateTime, formatRelativeTime } from "../../lib/format";

const ONE_HOUR_MS = 60 * 60 * 1000;

/** A count capped by a query's own `limit` reads honestly as "100+" (not a
 * bare "100" that looks exact) whenever next_cursor says there's more —
 * this page does client-side aggregation over one page of results, not
 * full pagination-following, so silently under-reporting past the cap
 * would be worse than admitting the cap exists. */
function countLabel(count: number, hasMore: boolean): string {
  return hasMore ? `${count}+` : String(count);
}

/** Home page for the console — not one of PRD §7's 4 required surfaces,
 * built as agreed bonus scope matching the reference mockup. Every stat
 * here is a real aggregate over data the REST API actually returns (no
 * dedicated "dashboard summary" route exists) — queue_depth summed and
 * recent_success_rate averaged across GET /endpoints, "events published"
 * counted from GET /events?from=. There's no tenant-wide "recent
 * deliveries" endpoint (only per-endpoint or per-event), so this shows
 * recent *events* instead of inventing an N+1 fan-out fetch for a summary
 * page. */
export function Overview() {
  const client = useApiClient();
  const { backend } = useBackend();
  const navigate = useNavigate();

  const endpointsQuery = useQuery({
    queryKey: ["endpoints", backend.id],
    queryFn: () => client!.listEndpoints({ limit: 100 }),
    enabled: client !== null,
    refetchInterval: 3000,
  });

  // Unlike Events.tsx's query key (which carries the actual filter value
  // as its distinguishing suffix), these two intentionally use fixed
  // labels — they aren't simple filters, they're two fixed-purpose
  // fetches (a small sample vs. a 1h-windowed count) that happen to both
  // hit GET /events.
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

  const isLoading = endpointsQuery.isLoading || eventsQuery.isLoading || recentEventsQuery.isLoading;
  const firstError = endpointsQuery.error ?? eventsQuery.error ?? recentEventsQuery.error;

  const endpoints = endpointsQuery.data?.endpoints ?? [];
  const pendingTotal = endpoints.reduce((sum, e) => sum + e.queue_depth, 0);
  const withSuccessRate = endpoints.filter((e) => e.recent_success_rate !== null);
  // Simple average across endpoints, not weighted by each endpoint's
  // delivery volume — the API returns a bare ratio per endpoint
  // (recent_success_rate), not the counts a true weighted mean would
  // need, so "average across endpoints" is the honest label for what
  // this actually is.
  const avgSuccessRate =
    withSuccessRate.length === 0
      ? null
      : withSuccessRate.reduce((sum, e) => sum + e.recent_success_rate!, 0) / withSuccessRate.length;
  const needingAttention = endpoints.filter((e) => e.status === "halted" || e.status === "paused");

  return (
    <div>
      <h1 style={{ fontSize: 28, marginBottom: 4 }}>Delivery health</h1>
      <p style={{ fontSize: 13, color: "var(--color-text-muted)", marginBottom: 20 }}>Polling every 3s · {backend.label}</p>

      {isLoading && <LoadingState />}
      {firstError && <ErrorState message={`Failed to load overview: ${(firstError as Error).message}`} />}

      {!isLoading && !firstError && (
        <>
          <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 16, marginBottom: 24 }}>
            <StatCard
              label="Events published (1h)"
              value={countLabel(recentEventsQuery.data!.events.length, recentEventsQuery.data!.next_cursor !== null)}
            />
            <StatCard label="Deliveries pending" value={pendingTotal} />
            <StatCard
              label="Success rate (endpoint avg)"
              value={avgSuccessRate === null ? "—" : `${Math.round(avgSuccessRate * 100)}%`}
            />
            <StatCard label="Endpoints needing you" value={needingAttention.length} />
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
              {eventsQuery.data!.events.length === 0 ? (
                <Card>
                  <p style={{ margin: 0, color: "var(--color-text-muted)", fontSize: 13 }}>Nothing published yet.</p>
                </Card>
              ) : (
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
                      {eventsQuery.data!.events.map((event) => (
                        <tr key={event.id} className="clickable" onClick={() => navigate(`/console/events/${event.id}`)}>
                          <td className="app-mono">{event.id}</td>
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
        </>
      )}
    </div>
  );
}
