import type pg from "pg";
import type http from "node:http";
import type https from "node:https";
import { decryptSecret } from "../lib/crypto.js";
import { resolveAndPin, sendOutboundRequest, type ResolveAndPinResult, type NetworkErrorClass } from "./httpClient.js";
import { signPayload } from "../lib/signing.js";
import { lease, outboundHttp, backoff } from "../config.js";

// Full jitter (Attempt ceiling & backoff schedule ticket, AWS-documented
// retry-storm mitigation): delay = random(0, min(base * multiplier^(attempt
// - 1), cap)). attemptNumber is 1-indexed — the attempt that just failed —
// so attempt 1's ceiling is the base delay, not multiplied yet.
export function computeBackoffDelayMs(
  attemptNumber: number,
  config: { baseDelayMs: number; multiplier: number; maxDelayMs: number },
): number {
  const ceiling = Math.min(config.baseDelayMs * config.multiplier ** (attemptNumber - 1), config.maxDelayMs);
  return Math.floor(Math.random() * ceiling);
}

export interface ClaimedDelivery {
  endpointId: string;
  tenantId: string;
  leaseId: string;
  deliveryId: string;
  eventId: string;
  eventType: string;
  payload: unknown;
  attemptNumber: number;
  attemptId: string;
  url: string;
  signingSecret: string;
  secondarySecret: string | null;
}

interface CandidateRow {
  id: string;
  tenant_id: string;
  busy: boolean;
}

// ADR-002/ADR-003/ADR-007: claim query orders by tenant fairness
// (least-recently-served first) then the endpoint's oldest ready delivery.
// Claiming is a short-lived row lock (the busy flag), not held for the
// outbound HTTP call — that happens entirely outside this function. Passive
// lease reclaim (DECISIONS.md: "folded into the normal claim query, no
// reaper") is handled inline: an endpoint whose busy_since is past the
// lease duration is claimable again, and if it had an attempt in flight,
// that attempt is closed with a synthetic worker_lease_expired outcome and
// its delivery requeued before the new claim proceeds.
export async function claimDelivery(pool: pg.Pool, leaseDurationMs: number): Promise<ClaimedDelivery | null> {
  const cutoff = new Date(Date.now() - leaseDurationMs);

  const { rows: candidates } = await pool.query<CandidateRow>(
    `SELECT e.id, e.tenant_id, e.busy
     FROM endpoints e
     JOIN tenants t ON t.id = e.tenant_id
     WHERE e.status = 'active'
       AND (e.busy = false OR e.busy_since < $1)
       AND EXISTS (
         SELECT 1 FROM deliveries d
         WHERE d.endpoint_id = e.id
           AND ((d.state = 'pending' AND d.next_attempt_at <= now()) OR d.state = 'in_flight')
       )
     ORDER BY t.last_served_at ASC NULLS FIRST,
       (SELECT MIN(d.seq) FROM deliveries d
        WHERE d.endpoint_id = e.id
          AND ((d.state = 'pending' AND d.next_attempt_at <= now()) OR d.state = 'in_flight')) ASC
     LIMIT 20`,
    [cutoff],
  );

  for (const candidate of candidates) {
    const claimed = await tryClaimEndpoint(pool, candidate, cutoff);
    if (claimed) return claimed;
  }
  return null;
}

async function tryClaimEndpoint(pool: pg.Pool, candidate: CandidateRow, cutoff: Date): Promise<ClaimedDelivery | null> {
  const { rows: claimRows } = await pool.query<{ lease_id: string }>(
    `UPDATE endpoints
     SET busy = true, busy_since = now(), lease_id = gen_random_uuid()
     WHERE id = $1 AND status = 'active' AND (busy = false OR busy_since < $2)
     RETURNING lease_id`,
    [candidate.id, cutoff],
  );
  const claimRow = claimRows[0];
  if (!claimRow) return null; // lost the race to another worker
  const leaseId = claimRow.lease_id;

  await pool.query(`UPDATE tenants SET last_served_at = now() WHERE id = $1`, [candidate.tenant_id]);

  if (candidate.busy) {
    // Reclaiming a stale lease: the previous worker's attempt row (sent_at
    // set, no response fields yet) is the orphan R-17 says must not strand
    // the delivery.
    await pool.query(
      `UPDATE attempts SET error_class = 'worker_lease_expired'
       WHERE delivery_id = (SELECT id FROM deliveries WHERE endpoint_id = $1 AND state = 'in_flight')
         AND sent_at IS NOT NULL AND response_status IS NULL AND error_class IS NULL`,
      [candidate.id],
    );
    await pool.query(`UPDATE deliveries SET state = 'pending' WHERE endpoint_id = $1 AND state = 'in_flight'`, [candidate.id]);
  }

  interface DeliveryRow {
    id: string;
    event_id: string;
    attempt_count: number;
    event_type: string;
    payload: unknown;
    url: string;
    signing_secret: string;
    secondary_secret: string | null;
    secondary_secret_expires_at: Date | null;
  }

  const { rows: deliveryRows } = await pool.query<DeliveryRow>(
    `SELECT d.id, d.event_id, d.attempt_count, e.type AS event_type, e.payload,
            ep.url, ep.signing_secret, ep.secondary_secret, ep.secondary_secret_expires_at
     FROM deliveries d
     JOIN events e ON e.id = d.event_id
     JOIN endpoints ep ON ep.id = d.endpoint_id
     WHERE d.endpoint_id = $1 AND d.state = 'pending' AND d.next_attempt_at <= now()
     ORDER BY d.seq
     LIMIT 1`,
    [candidate.id],
  );
  const delivery = deliveryRows[0];
  if (!delivery) {
    // Candidate filter guarantees this shouldn't happen, but release the
    // claim defensively rather than leave the endpoint stuck busy.
    await pool.query(`UPDATE endpoints SET busy = false, busy_since = NULL, lease_id = NULL WHERE id = $1 AND lease_id = $2`, [
      candidate.id,
      leaseId,
    ]);
    return null;
  }

  const attemptNumber = delivery.attempt_count + 1;
  const { rows: attemptRows } = await pool.query<{ id: string }>(
    `INSERT INTO attempts (delivery_id, attempt_number, sent_at) VALUES ($1, $2, now()) RETURNING id`,
    [delivery.id, attemptNumber],
  );
  await pool.query(`UPDATE deliveries SET state = 'in_flight', attempt_count = $2 WHERE id = $1`, [delivery.id, attemptNumber]);

  const hasActiveSecondary =
    delivery.secondary_secret !== null &&
    delivery.secondary_secret_expires_at !== null &&
    delivery.secondary_secret_expires_at > new Date();

  return {
    endpointId: candidate.id,
    tenantId: candidate.tenant_id,
    leaseId,
    deliveryId: delivery.id,
    eventId: delivery.event_id,
    eventType: delivery.event_type,
    payload: delivery.payload,
    attemptNumber,
    attemptId: attemptRows[0]!.id,
    url: delivery.url,
    signingSecret: decryptSecret(delivery.signing_secret),
    secondarySecret: hasActiveSecondary ? decryptSecret(delivery.secondary_secret!) : null,
  };
}

// Extends httpClient's network-layer classes with the two outcomes that
// only make sense at this orchestration level.
export type AttemptErrorClass = NetworkErrorClass | "url_not_allowed" | "worker_lease_expired";

export interface AttemptOutcome {
  responseStatus: number | null;
  responseBodyTruncated: string;
  durationMs: number;
  errorClass: AttemptErrorClass | null;
}

export type CompleteDeliveryOutcome = "succeeded" | "retrying" | "halted" | "lease_lost";

export interface BackoffConfig {
  baseDelayMs: number;
  multiplier: number;
  maxDelayMs: number;
  maxAttempts: number;
}

// ADR-0002 fencing: every post-HTTP-call write is gated on the endpoint's
// current lease_id still matching what claimDelivery captured. A mismatch
// means another worker has since reclaimed this endpoint (this worker
// stalled past its lease, per ADR-0002/ADR-003) — the write is dropped
// silently rather than corrupting state the new owner already wrote. R-16's
// "2xx is success" (PRD §6) is the only success criterion; everything else,
// including no response at all, retries until the attempt ceiling halts it.
export async function completeDelivery(
  pool: pg.Pool,
  claimed: ClaimedDelivery,
  result: AttemptOutcome,
  backoffConfig: BackoffConfig,
): Promise<CompleteDeliveryOutcome> {
  const client = await pool.connect();
  try {
    await client.query("BEGIN");

    const { rows: fenceRows } = await client.query<{ lease_id: string | null }>(
      `SELECT lease_id FROM endpoints WHERE id = $1 FOR UPDATE`,
      [claimed.endpointId],
    );
    if (fenceRows[0]?.lease_id !== claimed.leaseId) {
      await client.query("ROLLBACK");
      return "lease_lost";
    }

    await client.query(
      `UPDATE attempts
       SET response_status = $2, response_body_truncated = $3, duration_ms = $4, error_class = $5
       WHERE id = $1`,
      [claimed.attemptId, result.responseStatus, result.responseBodyTruncated, result.durationMs, result.errorClass],
    );

    const success = result.responseStatus !== null && result.responseStatus >= 200 && result.responseStatus < 300;

    let outcome: CompleteDeliveryOutcome;
    if (success) {
      await client.query(`UPDATE deliveries SET state = 'succeeded' WHERE id = $1`, [claimed.deliveryId]);
      outcome = "succeeded";
    } else if (claimed.attemptNumber >= backoffConfig.maxAttempts) {
      await client.query(`UPDATE deliveries SET state = 'failed' WHERE id = $1`, [claimed.deliveryId]);
      await client.query(`UPDATE endpoints SET status = 'halted' WHERE id = $1`, [claimed.endpointId]);
      outcome = "halted";
    } else {
      const delayMs = computeBackoffDelayMs(claimed.attemptNumber, backoffConfig);
      await client.query(
        `UPDATE deliveries SET state = 'pending', next_attempt_at = now() + ($2 * interval '1 millisecond') WHERE id = $1`,
        [claimed.deliveryId, delayMs],
      );
      outcome = "retrying";
    }

    await client.query(`UPDATE endpoints SET busy = false, busy_since = NULL, lease_id = NULL WHERE id = $1`, [claimed.endpointId]);

    await client.query("COMMIT");
    return outcome;
  } catch (err) {
    await client.query("ROLLBACK");
    throw err;
  } finally {
    client.release();
  }
}

export interface DeliveryCycleDeps {
  // Test seam: defaults to the real resolveAndPin. A test that wants to
  // exercise the real SSRF logic with a controlled DNS answer overrides
  // this with `(hostname) => resolveAndPin(hostname, stubResolver)` — the
  // stub-resolver pattern REVIEW.md itself prescribes for SSRF fixtures. A
  // happy-path test against a real local receiver has no choice but to
  // override this wholesale instead: the receiver is necessarily on
  // loopback, which the real check correctly always rejects (ADR-0006), so
  // there is no DNS answer that both resolves there and passes validation.
  resolveAndPin?: (hostname: string) => Promise<ResolveAndPinResult>;
  // Per-host connection limit (R-16) via http.Agent/https.Agent's maxSockets,
  // which caps concurrent sockets per host:port already — no custom limiter
  // needed. Split by protocol since an http.Agent can't serve an https
  // request and vice versa.
  agents?: { http: http.Agent; https: https.Agent };
}

// Orchestrates one claim -> sign -> resolve-and-pin -> send -> write-back
// cycle. A delivery-time SSRF rejection (R-2's "re-validated at delivery
// time, since DNS can change after registration") is treated as an
// ordinary retryable failure — same ceiling and backoff as any other
// non-2xx outcome — rather than a special case.
export async function runDeliveryCycle(pool: pg.Pool, deps: DeliveryCycleDeps = {}): Promise<boolean> {
  const claimed = await claimDelivery(pool, lease.durationMs);
  if (!claimed) return false;

  const url = new URL(claimed.url);
  const resolvePin = deps.resolveAndPin ?? ((hostname: string) => resolveAndPin(hostname));
  const pin = await resolvePin(url.hostname);

  let result: AttemptOutcome;
  if (!pin.allowed) {
    result = { responseStatus: null, responseBodyTruncated: "", durationMs: 0, errorClass: "url_not_allowed" };
  } else {
    const timestamp = Math.floor(Date.now() / 1000);
    const rawBody = JSON.stringify(claimed.payload);
    const secrets = claimed.secondarySecret ? [claimed.signingSecret, claimed.secondarySecret] : [claimed.signingSecret];
    const agent = url.protocol === "https:" ? deps.agents?.https : deps.agents?.http;

    result = await sendOutboundRequest(pin.ip, url, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "webhook-id": claimed.deliveryId,
        "webhook-event-id": claimed.eventId,
        "webhook-attempt": String(claimed.attemptNumber),
        "webhook-timestamp": String(timestamp),
        "webhook-signature": signPayload(secrets, timestamp, rawBody),
      },
      body: rawBody,
      connectTimeoutMs: outboundHttp.connectTimeoutMs,
      totalTimeoutMs: outboundHttp.totalTimeoutMs,
      maxResponseBodyBytes: outboundHttp.maxResponseBodyBytes,
      agent,
    });
  }

  await completeDelivery(pool, claimed, result, backoff);
  return true;
}
