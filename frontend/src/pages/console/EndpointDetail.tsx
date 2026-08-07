import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { useApiClient } from "../../api/useApiClient";
import { useBackend } from "../../lib/backend";
import { Badge } from "../../design/Badge";
import { Button } from "../../design/Button";
import { Card } from "../../design/Card";
import "../../design/Table.css";
import { formatDateTime, formatPercent, formatRelativeTime } from "../../lib/format";
import { RevealedSecretModal, ResumeDisclosureModal } from "./EndpointActionModals";
import { useEndpointActions } from "./useEndpointActions";
import type { DeliveryState } from "../../api/types";

function deliveryTone(state: DeliveryState): "neutral" | "accent" | "danger" {
  if (state === "failed") return "danger";
  if (state === "in_flight" || state === "succeeded") return "accent";
  return "neutral";
}

export function EndpointDetail() {
  const { id } = useParams<{ id: string }>();
  const client = useApiClient();
  const { backend } = useBackend();

  const endpointQuery = useQuery({
    queryKey: ["endpoints", backend.id, id],
    queryFn: () => client!.getEndpoint(id!),
    enabled: client !== null && !!id,
    refetchInterval: 3000,
  });

  const queueQuery = useQuery({
    queryKey: ["endpoint-deliveries", backend.id, id],
    queryFn: () => client!.listEndpointDeliveries(id!, { limit: 50 }),
    enabled: client !== null && !!id,
    refetchInterval: 3000,
  });

  const {
    pauseMutation,
    resumeMutation,
    rotateMutation,
    revealedSecret,
    setRevealedSecret,
    resumeDisclosure,
    setResumeDisclosure,
    actionError,
    setActionError,
  } = useEndpointActions(["endpoints", backend.id, id]);

  if (!client) return null;
  const endpoint = endpointQuery.data;
  const queue = queueQuery.data;
  const busy = pauseMutation.isPending || resumeMutation.isPending || rotateMutation.isPending;

  return (
    <div>
      <p style={{ fontSize: 13, color: "var(--color-text-muted)", marginBottom: 8 }}>
        <Link to="/console/endpoints">Endpoints</Link>
      </p>

      {endpointQuery.isLoading && <p>Loading…</p>}
      {endpointQuery.isError && (
        <p style={{ color: "var(--color-danger)" }}>Failed to load endpoint: {(endpointQuery.error as Error).message}</p>
      )}
      {actionError && (
        <p style={{ color: "var(--color-danger)" }}>
          {actionError}{" "}
          <button
            onClick={() => setActionError(null)}
            style={{ background: "none", border: "none", cursor: "pointer", color: "var(--color-accent-700)", padding: 0 }}
          >
            Dismiss
          </button>
        </p>
      )}

      {endpoint && (
        <>
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 8 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
              <h1 style={{ fontSize: 24 }}>{endpoint.url}</h1>
              <Badge tone={endpoint.status === "halted" ? "danger" : endpoint.status === "active" ? "accent" : "neutral"}>
                {endpoint.status}
              </Badge>
            </div>
            <div style={{ display: "flex", gap: 8 }}>
              {endpoint.status === "active" ? (
                <Button disabled={busy} onClick={() => pauseMutation.mutate(endpoint.id)}>
                  Pause
                </Button>
              ) : (
                <Button disabled={busy} onClick={() => resumeMutation.mutate(endpoint.id)}>
                  Resume…
                </Button>
              )}
              <Button disabled={busy} onClick={() => rotateMutation.mutate({ id: endpoint.id, url: endpoint.url })}>
                Rotate secret
              </Button>
            </div>
          </div>
          <p style={{ fontSize: 13, color: "var(--color-text-muted)", marginBottom: 20 }}>
            {endpoint.event_types.join(", ")}
          </p>

          {endpoint.status === "halted" && (
            <Card style={{ marginBottom: 20, borderColor: "var(--color-danger)" }}>
              <h3 style={{ fontSize: 13, marginBottom: 8, color: "var(--color-danger)" }}>HALTED AT THE ATTEMPT CEILING</h3>
              <p style={{ fontSize: 13, margin: 0 }}>
                The head delivery exhausted its attempts, so this endpoint halted and everything behind it reports
                Blocked. Nothing was discarded — resume to skip the failed head and drain the rest, or replay to retry
                past events without disturbing the live queue.
              </p>
            </Card>
          )}

          <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 16, marginBottom: 20 }}>
            <Field label="Queue depth" value={String(endpoint.queue_depth)} />
            <Field label="Oldest pending" value={formatRelativeTime(endpoint.oldest_pending_at)} />
            <Field label="Success rate" value={formatPercent(endpoint.recent_success_rate)} />
            <Field label="Registered" value={formatDateTime(endpoint.created_at)} />
          </div>
        </>
      )}

      <h2 style={{ fontSize: 18, marginBottom: 12 }}>Pending deliveries, in publication order</h2>
      {queueQuery.isLoading && <p>Loading…</p>}
      {queue && queue.deliveries.length === 0 && (
        <Card>
          <p style={{ margin: 0, color: "var(--color-text-muted)" }}>Nothing in this endpoint's queue right now.</p>
        </Card>
      )}
      {queue && queue.deliveries.length > 0 && (
        <Card cornerMarks style={{ padding: 0 }}>
          <table className="app-table">
            <thead>
              <tr>
                <th>#</th>
                <th>Delivery</th>
                <th>State</th>
                <th>Next attempt</th>
              </tr>
            </thead>
            <tbody>
              {queue.deliveries.map((d, i) => (
                <tr key={d.id}>
                  <td>{String(i + 1).padStart(2, "0")}</td>
                  <td>
                    <Link to={`/console/deliveries/${d.id}`} className="app-mono">
                      {d.id}
                    </Link>
                  </td>
                  <td>
                    <Badge tone={deliveryTone(d.state)}>{d.state}</Badge>
                    {d.blocked_on_delivery_id && (
                      <span style={{ fontSize: 12, color: "var(--color-text-muted)", marginLeft: 8 }}>
                        blocked on{" "}
                        <Link to={`/console/deliveries/${d.blocked_on_delivery_id}`} className="app-mono">
                          {d.blocked_on_delivery_id}
                        </Link>
                      </span>
                    )}
                    {i === 0 && (
                      <span style={{ fontSize: 12, color: "var(--color-text-muted)", marginLeft: 8 }}>head of queue</span>
                    )}
                  </td>
                  <td>{d.state === "pending" || d.state === "in_flight" ? formatRelativeTime(d.next_attempt_at) : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}

      {revealedSecret && <RevealedSecretModal secret={revealedSecret} onClose={() => setRevealedSecret(null)} />}
      {resumeDisclosure && <ResumeDisclosureModal result={resumeDisclosure} onClose={() => setResumeDisclosure(null)} />}
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div style={{ fontSize: 11, letterSpacing: 0.4, textTransform: "uppercase", color: "var(--color-text-muted)" }}>
        {label}
      </div>
      <div style={{ fontSize: 14 }}>{value}</div>
    </div>
  );
}
