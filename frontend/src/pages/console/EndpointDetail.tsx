import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { useApiClient } from "../../api/useApiClient";
import { useBackend } from "../../lib/backend";
import { Badge } from "../../design/Badge";
import { Breadcrumb } from "../../design/Breadcrumb";
import { Button } from "../../design/Button";
import { Card } from "../../design/Card";
import { Field } from "../../design/Field";
import "../../design/Table.css";
import { deliveryTone, nextAttemptDisplay } from "../../lib/deliveryDisplay";
import { formatDateTime, formatPercent, formatRelativeTime } from "../../lib/format";
import { ActionErrorBanner, RevealedSecretModal } from "./EndpointActionModals";
import { useEndpointActions } from "./useEndpointActions";

export function EndpointDetail() {
  const { id } = useParams<{ id: string }>();
  const client = useApiClient();
  const { backend } = useBackend();

  const endpointQueryKey = ["endpoints", backend.id, id];
  const queueQueryKey = ["endpoint-deliveries", backend.id, id];

  const endpointQuery = useQuery({
    queryKey: endpointQueryKey,
    queryFn: () => client!.getEndpoint(id!),
    enabled: client !== null && !!id,
    refetchInterval: 3000,
  });

  const queueQuery = useQuery({
    queryKey: queueQueryKey,
    queryFn: () => client!.listEndpointDeliveries(id!, { limit: 50 }),
    enabled: client !== null && !!id,
    refetchInterval: 3000,
  });

  // Resume isn't wired in here — the interactive resume flow with its R-14
  // ordering-consequence disclosure, alongside this queue view, is #30's
  // scope. Pause/rotate are kept as basic endpoint-management parity with
  // the endpoints list (#28).
  const { pauseMutation, rotateMutation, revealedSecret, setRevealedSecret, actionError, setActionError } =
    useEndpointActions([endpointQueryKey, queueQueryKey]);

  if (!client) return null;
  const endpoint = endpointQuery.data;
  const queue = queueQuery.data;
  const busy = pauseMutation.isPending || rotateMutation.isPending;

  return (
    <div>
      <Breadcrumb>
        <Link to="/console/endpoints">Endpoints</Link>
      </Breadcrumb>

      {endpointQuery.isLoading && <p>Loading…</p>}
      {endpointQuery.isError && (
        <p style={{ color: "var(--color-danger)" }}>Failed to load endpoint: {(endpointQuery.error as Error).message}</p>
      )}
      {actionError && <ActionErrorBanner message={actionError} onDismiss={() => setActionError(null)} />}

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
              {endpoint.status === "active" && (
                <Button disabled={busy} onClick={() => pauseMutation.mutate(endpoint.id)}>
                  Pause
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
                The head delivery (highlighted below) exhausted its attempts, so this endpoint halted and everything
                behind it reports Blocked. Nothing was discarded — resume from the endpoints list to skip the failed
                head and drain the rest, or replay to retry past events without disturbing the live queue.
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
              {queue.deliveries.map((d, i) => {
                const isHead = i === 0;
                // "the head highlighted when halted" (PRD §7) — the head
                // is always labelled, but only gets a visual highlight
                // when it's actually the reason everything behind it is
                // stuck (i.e. the endpoint has halted on it).
                const highlight = isHead && endpoint?.status === "halted";
                return (
                  <tr key={d.id} style={highlight ? { background: "var(--color-accent-100)" } : undefined}>
                    <td>{String(i + 1).padStart(2, "0")}</td>
                    <td>
                      <Link to={`/console/deliveries/${d.id}`} className="app-mono">
                        {d.id}
                      </Link>
                    </td>
                    <td>
                      <Badge tone={deliveryTone(d.state)}>{d.state}</Badge>
                      {isHead && (
                        <span style={{ fontSize: 12, fontWeight: highlight ? 600 : 400, marginLeft: 8 }}>
                          head of queue
                        </span>
                      )}
                      {d.blocked_on_delivery_id && (
                        <span style={{ fontSize: 12, color: "var(--color-text-muted)", marginLeft: 8 }}>
                          blocked on{" "}
                          <Link to={`/console/deliveries/${d.blocked_on_delivery_id}`} className="app-mono">
                            {d.blocked_on_delivery_id}
                          </Link>
                        </span>
                      )}
                    </td>
                    <td>{nextAttemptDisplay(d.state, d.next_attempt_at)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </Card>
      )}

      {revealedSecret && <RevealedSecretModal secret={revealedSecret} onClose={() => setRevealedSecret(null)} />}
    </div>
  );
}
