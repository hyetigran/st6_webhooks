import "dotenv/config";

function envInt(name: string, fallback: number): number {
  const raw = process.env[name];
  if (raw === undefined || raw === "") return fallback;
  const parsed = Number(raw);
  if (!Number.isFinite(parsed)) {
    throw new Error(`Env var ${name} must be a number, got "${raw}"`);
  }
  return parsed;
}

function envString(name: string, fallback?: string): string {
  const raw = process.env[name];
  if (raw === undefined || raw === "") {
    if (fallback === undefined) {
      throw new Error(`Env var ${name} is required`);
    }
    return fallback;
  }
  return raw;
}

// Retry backoff (ADR: Attempt ceiling & backoff schedule). Full jitter:
// delay = random(0, min(baseDelayMs * multiplier ** attempt, maxDelayMs)).
// Global config only for v0.2.0 — per-endpoint tuning is explicitly out of scope.
export const backoff = {
  baseDelayMs: envInt("BACKOFF_BASE_DELAY_MS", 1_000),
  multiplier: envInt("BACKOFF_MULTIPLIER", 2),
  maxDelayMs: envInt("BACKOFF_MAX_DELAY_MS", 30_000),
  maxAttempts: envInt("BACKOFF_MAX_ATTEMPTS", 6),
};

// Outbound HTTP call bounds (R-16). No maxRedirects — docs/adr/0006 replaced
// "bounded redirect count" with "never follow redirects" (a hop-count limit
// bounds count, not destination; a registered URL that 302s to a metadata
// address sails through untouched), so there's nothing to bound here.
export const outboundHttp = {
  connectTimeoutMs: envInt("OUTBOUND_CONNECT_TIMEOUT_MS", 5_000),
  totalTimeoutMs: envInt("OUTBOUND_TOTAL_TIMEOUT_MS", 10_000),
  maxResponseBodyBytes: envInt("OUTBOUND_MAX_RESPONSE_BODY_BYTES", 64 * 1024),
  maxConnectionsPerHost: envInt("OUTBOUND_MAX_CONNECTIONS_PER_HOST", 10),
};

// Lease duration for crash recovery (ADR-003): derived from the outbound HTTP
// timeout, not an independent constant, so the two can't drift apart. The
// 30s floor is itself env-configurable (not just outboundHttp.totalTimeoutMs)
// so chaos tests can shrink lease expiry to a few seconds instead of always
// waiting out the production-safe default — PRD §8's make verify "time is
// injected" applies here too, not just to backoff/outbound timeouts.
export const lease = {
  get durationMs(): number {
    const timeout = outboundHttp.totalTimeoutMs;
    const floor = envInt("LEASE_MIN_DURATION_MS", 30_000);
    return timeout + Math.max(timeout, floor);
  },
};

// Receiver contract signing (ADR-006).
export const signing = {
  timestampToleranceSec: envInt("SIGNATURE_TIMESTAMP_TOLERANCE_SEC", 5 * 60),
};

// Secret rotation overlap window (R-3 / ADR-0003): the sender signs with both
// the current and secondary secret until secondary_secret_expires_at, which is
// set to now + this window at rotation time. Not just informational — this
// value determines how long the old secret keeps working.
export const secretRotation = {
  overlapHours: envInt("SECRET_ROTATION_OVERLAP_HOURS", 24),
};

// Signing secrets are encrypted at rest with this key (AES-256-GCM) — a DB
// dump alone shouldn't hand over every customer's signing secret in
// plaintext (REVIEW.md F-16). Required, 32 bytes, base64-encoded. No
// hardcoded fallback, even for local dev — generate one with
// `openssl rand -base64 32` and put it in .env.
export const secretEncryptionKey = {
  keyBuffer(): Buffer {
    const raw = envString("SECRET_ENCRYPTION_KEY");
    const buf = Buffer.from(raw, "base64");
    if (buf.length !== 32) {
      throw new Error("SECRET_ENCRYPTION_KEY must decode to exactly 32 bytes (base64-encoded)");
    }
    return buf;
  },
};

export const server = {
  port: envInt("PORT", 3000),
};

export const worker = {
  // How long to wait before polling again after a cycle found nothing to
  // do. 0 when a cycle did find work, so the worker drains a backlog
  // immediately rather than waiting out a full idle interval per item.
  idlePollIntervalMs: envInt("WORKER_IDLE_POLL_INTERVAL_MS", 200),
};

export const db = {
  connectionString: envString("DATABASE_URL", "postgres://webhooks:webhooks@localhost:5532/webhooks_node"),
};
