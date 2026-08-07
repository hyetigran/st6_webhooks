import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useApiClient } from "../../api/useApiClient";
import type { EndpointWithHealth, RegisterEndpointResponse, ResumeEndpointResponse, RotateSecretResponse } from "../../api/types";
import { useBackend } from "../../lib/backend";
import { Badge } from "../../design/Badge";
import { Button } from "../../design/Button";
import { Card } from "../../design/Card";
import { Modal } from "../../design/Modal";
import "../../design/Table.css";
import { formatDateTime, formatPercent, formatRelativeTime } from "../../lib/format";

type RevealedSecret = { url: string; secret: string; overlapExpiresAt?: string };

export function Endpoints() {
  const client = useApiClient();
  const { backend } = useBackend();
  const queryClient = useQueryClient();
  const queryKey = ["endpoints", backend.id];

  const [showRegister, setShowRegister] = useState(false);
  const [revealedSecret, setRevealedSecret] = useState<RevealedSecret | null>(null);
  const [resumeDisclosure, setResumeDisclosure] = useState<ResumeEndpointResponse | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const onActionError = (err: unknown) => setActionError(err instanceof Error ? err.message : "Action failed");

  const { data, isLoading, isError, error } = useQuery({
    queryKey,
    queryFn: () => client!.listEndpoints({ limit: 100 }),
    enabled: client !== null,
    refetchInterval: 3000,
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey });

  const registerMutation = useMutation({
    mutationFn: (input: { url: string; event_types: string[] }) => client!.registerEndpoint(input),
    onSuccess: (endpoint: RegisterEndpointResponse) => {
      setShowRegister(false);
      setRevealedSecret({ url: endpoint.url, secret: endpoint.signing_secret });
      invalidate();
    },
  });

  const pauseMutation = useMutation({
    mutationFn: (id: string) => client!.pauseEndpoint(id),
    onSuccess: invalidate,
    onError: onActionError,
  });

  const resumeMutation = useMutation({
    mutationFn: (id: string) => client!.resumeEndpoint(id),
    onSuccess: (result: ResumeEndpointResponse) => {
      setResumeDisclosure(result);
      invalidate();
    },
    onError: onActionError,
  });

  const rotateMutation = useMutation({
    mutationFn: (id: string) => client!.rotateSecret(id),
    onSuccess: (result: RotateSecretResponse, id: string) => {
      const endpoint = data?.endpoints.find((e) => e.id === id);
      setRevealedSecret({ url: endpoint?.url ?? id, secret: result.signing_secret, overlapExpiresAt: result.overlap_expires_at });
    },
    onError: onActionError,
  });

  const busyEndpointId = [pauseMutation, resumeMutation, rotateMutation].find((m) => m.isPending)?.variables ?? null;

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
                  onRotate={() => rotateMutation.mutate(endpoint.id)}
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

      {revealedSecret && (
        <Modal onClose={() => setRevealedSecret(null)}>
          <h2 style={{ fontSize: 18, marginBottom: 8 }}>Signing secret</h2>
          <p style={{ fontSize: 13, color: "var(--color-text-muted)" }}>
            Shown once for <strong>{revealedSecret.url}</strong> — copy it now, it will not be shown again.
            {revealedSecret.overlapExpiresAt && (
              <> The previous secret keeps verifying until {formatDateTime(revealedSecret.overlapExpiresAt)}.</>
            )}
          </p>
          <code
            style={{
              display: "block",
              fontFamily: "var(--font-mono)",
              fontSize: 13,
              padding: "10px 12px",
              background: "var(--color-badge-bg)",
              wordBreak: "break-all",
              margin: "12px 0",
            }}
          >
            {revealedSecret.secret}
          </code>
          <Button variant="primary" onClick={() => setRevealedSecret(null)}>
            Done
          </Button>
        </Modal>
      )}

      {resumeDisclosure && (
        <Modal onClose={() => setResumeDisclosure(null)}>
          <h2 style={{ fontSize: 18, marginBottom: 8 }}>Resumed</h2>
          <p style={{ fontSize: 13 }}>
            {resumeDisclosure.pending_delivery_count} deliveries are pending and will drain in publication order.
          </p>
          {resumeDisclosure.skipped_failed_delivery_ids.length > 0 && (
            <p style={{ fontSize: 13, color: "var(--color-danger)" }}>
              {resumeDisclosure.skipped_failed_delivery_ids.length} previously-failed deliveries are permanently skipped
              (never retried by resume — only replay would): {resumeDisclosure.skipped_failed_delivery_ids.join(", ")}
            </p>
          )}
          <Button variant="primary" onClick={() => setResumeDisclosure(null)}>
            Done
          </Button>
        </Modal>
      )}
    </div>
  );
}

function EndpointRow({
  endpoint,
  onPause,
  onResume,
  onRotate,
  busy,
}: {
  endpoint: EndpointWithHealth;
  onPause: () => void;
  onResume: () => void;
  onRotate: () => void;
  busy: boolean;
}) {
  return (
    <tr>
      <td>
        <div style={{ fontWeight: 600 }}>{endpoint.url}</div>
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
