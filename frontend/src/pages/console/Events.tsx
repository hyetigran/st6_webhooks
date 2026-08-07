import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useApiClient } from "../../api/useApiClient";
import { useBackend } from "../../lib/backend";
import { Card } from "../../design/Card";
import { ErrorState } from "../../design/ErrorState";
import { LoadingState } from "../../design/LoadingState";
import { TextInput } from "../../design/TextInput";
import "../../design/Table.css";
import { formatDateTime } from "../../lib/format";

export function Events() {
  const client = useApiClient();
  const { backend } = useBackend();
  const navigate = useNavigate();
  const [type, setType] = useState("");
  // Reachable from an endpoint's queue view ("View event history") — the
  // API client has always supported this filter (Shared REST API
  // contract, R-24), it just had no UI path to it until now.
  const [searchParams, setSearchParams] = useSearchParams();
  const endpointId = searchParams.get("endpoint_id");

  const queryKey = ["events", backend.id, type, endpointId];
  const { data, isLoading, isError, error } = useQuery({
    queryKey,
    queryFn: () => client!.listEvents({ limit: 50, type: type || undefined, endpoint_id: endpointId ?? undefined }),
    enabled: client !== null,
    refetchInterval: 3000,
  });

  if (!client) return null;

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 20 }}>
        <h1 style={{ fontSize: 28 }}>Events</h1>
        <TextInput type="text" placeholder="Filter by type…" value={type} onChange={(e) => setType(e.target.value)} />
      </div>

      {endpointId && (
        <p style={{ fontSize: 13, color: "var(--color-text-muted)", marginBottom: 12 }}>
          Filtered to endpoint <span className="app-mono">{endpointId}</span> —{" "}
          <button
            onClick={() => setSearchParams({})}
            style={{ background: "none", border: "none", cursor: "pointer", color: "var(--color-accent-700)", padding: 0 }}
          >
            clear
          </button>
        </p>
      )}

      {isLoading && <LoadingState />}
      {isError && <ErrorState message={`Failed to load events: ${(error as Error).message}`} />}

      {data && data.events.length === 0 && (
        <Card>
          <p style={{ margin: 0, color: "var(--color-text-muted)" }}>No events published yet on {backend.label}.</p>
        </Card>
      )}

      {data && data.events.length > 0 && (
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
              {data.events.map((event) => (
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
  );
}
