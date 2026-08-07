import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useApiClient } from "../../api/useApiClient";
import { useBackend } from "../../lib/backend";
import { Card } from "../../design/Card";
import "../../design/Table.css";
import { formatDateTime } from "../../lib/format";

export function Events() {
  const client = useApiClient();
  const { backend } = useBackend();
  const navigate = useNavigate();
  const [type, setType] = useState("");

  const queryKey = ["events", backend.id, type];
  const { data, isLoading, isError, error } = useQuery({
    queryKey,
    queryFn: () => client!.listEvents({ limit: 50, type: type || undefined }),
    enabled: client !== null,
    refetchInterval: 3000,
  });

  if (!client) return null;

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 20 }}>
        <h1 style={{ fontSize: 28 }}>Events</h1>
        <input
          type="text"
          placeholder="Filter by type…"
          value={type}
          onChange={(e) => setType(e.target.value)}
          style={{
            fontFamily: "var(--font-body)",
            fontSize: 14,
            padding: "8px 10px",
            border: "1px solid var(--color-divider)",
            borderRadius: "var(--radius)",
          }}
        />
      </div>

      {isLoading && <p>Loading…</p>}
      {isError && <p style={{ color: "var(--color-danger)" }}>Failed to load events: {(error as Error).message}</p>}

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
