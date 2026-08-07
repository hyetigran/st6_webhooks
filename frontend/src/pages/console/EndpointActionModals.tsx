import { Button } from "../../design/Button";
import { Modal } from "../../design/Modal";
import { formatDateTime } from "../../lib/format";
import type { ResumeEndpointResponse } from "../../api/types";
import type { RevealedSecret } from "./useEndpointActions";

/** Shared by the endpoints list and the single-endpoint queue view — both
 * reveal a signing secret (register/rotate) and disclose resume's
 * ordering consequence (R-14) the same way. */
export function RevealedSecretModal({ secret, onClose }: { secret: RevealedSecret; onClose: () => void }) {
  return (
    <Modal onClose={onClose}>
      <h2 style={{ fontSize: 18, marginBottom: 8 }}>Signing secret</h2>
      <p style={{ fontSize: 13, color: "var(--color-text-muted)" }}>
        Shown once for <strong>{secret.url}</strong> — copy it now, it will not be shown again.
        {secret.overlapExpiresAt && <> The previous secret keeps verifying until {formatDateTime(secret.overlapExpiresAt)}.</>}
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
        {secret.secret}
      </code>
      <Button variant="primary" onClick={onClose}>
        Done
      </Button>
    </Modal>
  );
}

export function ResumeDisclosureModal({ result, onClose }: { result: ResumeEndpointResponse; onClose: () => void }) {
  return (
    <Modal onClose={onClose}>
      <h2 style={{ fontSize: 18, marginBottom: 8 }}>Resumed</h2>
      <p style={{ fontSize: 13 }}>{result.pending_delivery_count} deliveries are pending and will drain in publication order.</p>
      {result.skipped_failed_delivery_ids.length > 0 && (
        <p style={{ fontSize: 13, color: "var(--color-danger)" }}>
          {result.skipped_failed_delivery_ids.length} previously-failed deliveries are permanently skipped (never
          retried by resume — only replay would): {result.skipped_failed_delivery_ids.join(", ")}
        </p>
      )}
      <Button variant="primary" onClick={onClose}>
        Done
      </Button>
    </Modal>
  );
}
