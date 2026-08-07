import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useApiClient } from "../../api/useApiClient";
import type { EndpointWithHealth, RegisterEndpointResponse } from "../../api/types";
import { useBackend } from "../../lib/backend";
import { Badge } from "../../design/Badge";
import { Button } from "../../design/Button";
import { Card } from "../../design/Card";
import { Modal } from "../../design/Modal";
import "../../design/Table.css";
import { formatPercent, formatRelativeTime } from "../../lib/format";
import { ActionErrorBanner, RevealedSecretModal, ResumeDisclosureModal } from "./EndpointActionModals";
import { useEndpointActions } from "./useEndpointActions";
import { ReplayForm, ReplayResultModal } from "./ReplayModals";
import { useReplayAction } from "./useReplayAction";

export function Endpoints() {
  const client = useApiClient();
  const { backend } = useBackend();
  const queryClient = useQueryClient();
  const queryKey = ["endpoints", backend.id];

  const [showRegister, setShowRegister] = useState(false);

  const { data, isLoading, isError, error } = useQuery({
    queryKey,
    queryFn: () => client!.listEndpoints({ limit: 100 }),
    enabled: client !== null,
    refetchInterval: 3000,
  });

  const {
    pauseMutation,
    resumeMutation,
    rotateMutation,
    busyEndpointId,
    revealedSecret,
    setRevealedSecret,
    resumeDisclosure,
    setResumeDisclosure,
    actionError,
    setActionError,
  } = useEndpointActions([queryKey]);

  const { replayTarget, setReplayTarget, replayResult, setReplayResult, replayMutation } = useReplayAction([queryKey]);

  const registerMutation = useMutation({
    mutationFn: (input: { url: string; event_types: string[] }) => client!.registerEndpoint(input),
    onSuccess: (endpoint: RegisterEndpointResponse) => {
      setShowRegister(false);
      setRevealedSecret({ url: endpoint.url, secret: endpoint.signing_secret });
      queryClient.invalidateQueries({ queryKey });
    },
  });

  if (!client) return null;

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 20 }}>
        <h1 style={{ fontSize: 28 }}>Endpoints</h1>
        <Button variant="primary" onClick={() => setShowRegister(true)}>
          Register endpoint
        </Button>
      </div>

      {isLoading && <p>Loading…</p>}
      {isError && <p style={{ color: "var(--color-danger)" }}>Failed to load endpoints: {(error as Error).message}</p>}
      {actionError && <ActionErrorBanner message={actionError} onDismiss={() => setActionError(null)} />}

      {data && data.endpoints.length === 0 && (
        <Card>
          <p style={{ margin: 0, color: "var(--color-text-muted)" }}>
            No endpoints registered yet on {backend.label}. Register one to start receiving deliveries.
          </p>
        </Card>
      )}

      {data && data.endpoints.length > 0 && (
        <Card cornerMarks style={{ padding: 0 }}>
          <table className="app-table">
            <thead>
              <tr>
                <th>Endpoint</th>
                <th>Status</th>
                <th>Queue depth</th>
                <th>Oldest pending</th>
                <th>Success (24h)</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {data.endpoints.map((endpoint) => (
                <EndpointRow
                  key={endpoint.id}
                  endpoint={endpoint}
                  onPause={() => pauseMutation.mutate(endpoint.id)}
                  onResume={() => resumeMutation.mutate(endpoint.id)}
                  onRotate={() => rotateMutation.mutate({ id: endpoint.id, url: endpoint.url })}
                  onReplay={() => setReplayTarget({ id: endpoint.id, url: endpoint.url })}
                  busy={busyEndpointId === endpoint.id}
                />
              ))}
            </tbody>
          </table>
        </Card>
      )}

      {showRegister && (
        <Modal onClose={() => setShowRegister(false)}>
          <RegisterForm
            onSubmit={(input) => registerMutation.mutate(input)}
            error={registerMutation.isError ? (registerMutation.error as Error).message : null}
            submitting={registerMutation.isPending}
          />
        </Modal>
      )}

      {revealedSecret && <RevealedSecretModal secret={revealedSecret} onClose={() => setRevealedSecret(null)} />}
      {resumeDisclosure && <ResumeDisclosureModal result={resumeDisclosure} onClose={() => setResumeDisclosure(null)} />}
      {replayTarget && (
        <ReplayForm
          endpointUrl={replayTarget.url}
          onClose={() => setReplayTarget(null)}
          submitting={replayMutation.isPending}
          error={replayMutation.isError ? (replayMutation.error as Error).message : null}
          onSubmit={(range) => replayMutation.mutate({ endpointId: replayTarget.id, ...range })}
        />
      )}
      {replayResult && <ReplayResultModal result={replayResult} onClose={() => setReplayResult(null)} />}
    </div>
  );
}

function EndpointRow({
  endpoint,
  onPause,
  onResume,
  onRotate,
  onReplay,
  busy,
}: {
  endpoint: EndpointWithHealth;
  onPause: () => void;
  onResume: () => void;
  onRotate: () => void;
  onReplay: () => void;
  busy: boolean;
}) {
  return (
    <tr>
      <td>
        <Link to={`/console/endpoints/${endpoint.id}`} style={{ fontWeight: 600, color: "var(--color-text)" }}>
          {endpoint.url}
        </Link>
        <div style={{ fontSize: 12, color: "var(--color-text-muted)" }}>{endpoint.event_types.join(", ")}</div>
      </td>
      <td>
        <Badge tone={endpoint.status === "halted" ? "danger" : endpoint.status === "active" ? "accent" : "neutral"}>
          {endpoint.status}
        </Badge>
      </td>
      <td>{endpoint.queue_depth}</td>
      <td>{formatRelativeTime(endpoint.oldest_pending_at)}</td>
      <td>{formatPercent(endpoint.recent_success_rate)}</td>
      <td>
        <div style={{ display: "flex", gap: 8 }}>
          {endpoint.status === "active" ? (
            <Button onClick={onPause} disabled={busy}>
              Pause
            </Button>
          ) : (
            <Button onClick={onResume} disabled={busy}>
              Resume…
            </Button>
          )}
          <Button onClick={onRotate} disabled={busy}>
            Rotate secret
          </Button>
          <Button onClick={onReplay} disabled={busy}>
            Replay…
          </Button>
        </div>
      </td>
    </tr>
  );
}

function RegisterForm({
  onSubmit,
  error,
  submitting,
}: {
  onSubmit: (input: { url: string; event_types: string[] }) => void;
  error: string | null;
  submitting: boolean;
}) {
  const [url, setUrl] = useState("");
  const [eventTypes, setEventTypes] = useState("");

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const types = eventTypes
      .split(",")
      .map((t) => t.trim())
      .filter(Boolean);
    onSubmit({ url: url.trim(), event_types: types });
  }

  const inputStyle: React.CSSProperties = {
    fontFamily: "var(--font-body)",
    fontSize: 14,
    padding: "8px 10px",
    border: "1px solid var(--color-divider)",
    borderRadius: "var(--radius)",
  };

  return (
    <form onSubmit={handleSubmit} style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      <h2 style={{ fontSize: 18 }}>Register endpoint</h2>
      <label style={{ fontSize: 13, display: "flex", flexDirection: "column", gap: 4 }}>
        URL
        <input
          type="url"
          required
          placeholder="https://example.com/hook"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          style={inputStyle}
        />
      </label>
      <label style={{ fontSize: 13, display: "flex", flexDirection: "column", gap: 4 }}>
        Event types (comma-separated)
        <input
          type="text"
          required
          placeholder="order.created, invoice.paid"
          value={eventTypes}
          onChange={(e) => setEventTypes(e.target.value)}
          style={inputStyle}
        />
      </label>
      {error && <p style={{ color: "var(--color-danger)", fontSize: 13, margin: 0 }}>{error}</p>}
      <Button type="submit" variant="primary" disabled={submitting}>
        {submitting ? "Registering…" : "Register"}
      </Button>
    </form>
  );
}
