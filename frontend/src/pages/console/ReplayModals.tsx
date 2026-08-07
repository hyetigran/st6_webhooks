import { useState, type FormEvent } from "react";
import { Button } from "../../design/Button";
import { Modal } from "../../design/Modal";
import type { AsyncAcceptedResponse } from "../../api/types";

const inputStyle: React.CSSProperties = {
  fontFamily: "var(--font-body)",
  fontSize: 14,
  padding: "8px 10px",
  border: "1px solid var(--color-divider)",
  borderRadius: "var(--radius)",
};

export function ReplayForm({
  endpointUrl,
  onSubmit,
  error,
  submitting,
  onClose,
}: {
  endpointUrl: string;
  onSubmit: (range: { range_start: string; range_end: string }) => void;
  error: string | null;
  submitting: boolean;
  onClose: () => void;
}) {
  const [rangeStart, setRangeStart] = useState("");
  const [rangeEnd, setRangeEnd] = useState("");

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    // datetime-local has no timezone of its own; convert through Date so
    // the API always receives a real UTC "Z"-suffixed ISO string (the
    // backend's date parser requires the literal Z, not just any offset —
    // see progress.md's parseUTCDatetime gotcha).
    onSubmit({
      range_start: new Date(rangeStart).toISOString(),
      range_end: new Date(rangeEnd).toISOString(),
    });
  }

  return (
    <Modal onClose={onClose}>
      <form onSubmit={handleSubmit} style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        <h2 style={{ fontSize: 18 }}>Replay</h2>
        <p style={{ fontSize: 13, color: "var(--color-text-muted)", margin: 0 }}>
          Re-deliver every resolved delivery on <strong>{endpointUrl}</strong> published in this range, appended to
          the end of its live queue — other endpoints are unaffected, but a large replay can delay this endpoint's
          own upcoming traffic.
        </p>
        <label style={{ fontSize: 13, display: "flex", flexDirection: "column", gap: 4 }}>
          From
          <input type="datetime-local" required value={rangeStart} onChange={(e) => setRangeStart(e.target.value)} style={inputStyle} />
        </label>
        <label style={{ fontSize: 13, display: "flex", flexDirection: "column", gap: 4 }}>
          To
          <input type="datetime-local" required value={rangeEnd} onChange={(e) => setRangeEnd(e.target.value)} style={inputStyle} />
        </label>
        {error && <p style={{ color: "var(--color-danger)", fontSize: 13, margin: 0 }}>{error}</p>}
        <Button type="submit" variant="primary" disabled={submitting}>
          {submitting ? "Submitting…" : "Start replay"}
        </Button>
      </form>
    </Modal>
  );
}

export function ReplayResultModal({ result, onClose }: { result: AsyncAcceptedResponse; onClose: () => void }) {
  return (
    <Modal onClose={onClose}>
      <h2 style={{ fontSize: 18, marginBottom: 8 }}>Replay started</h2>
      <p style={{ fontSize: 13 }}>
        Replay <span className="app-mono">{result.id}</span> is <strong>{result.status}</strong>. The worker will scan
        the window and queue matching deliveries shortly — check the endpoint's queue to watch them arrive.
      </p>
      <Button variant="primary" onClick={onClose}>
        Done
      </Button>
    </Modal>
  );
}
