-- Initial schema for the webhook delivery service (Go stack).
-- Every table/field here traces to a decision recorded in ../../../../DECISIONS.md.
--
-- Mirrors node/src/db/migrations/001_init.sql + 002_deliveries_seq.sql as a
-- single migration: the Go stack never had a pre-seq schema to migrate away
-- from, so deliveries.seq (docs/adr/0007) is a plain column from the start
-- rather than a later ALTER TABLE.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE tenants (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name            TEXT NOT NULL,
  -- SHA-256 hash, not the plaintext key (REVIEW.md F-16) — a fast hash is
  -- correct here, not a slow password hash: the key is a high-entropy random
  -- token no one is guessing, so bcrypt/scrypt would only add latency to
  -- every authenticated request for no security benefit.
  api_key_hash    TEXT NOT NULL UNIQUE,
  -- Tenant fairness (docs/adr/0004-tenant-fairness-bound.md): least-
  -- recently-served-tenant ordering in the delivery worker's claim query.
  -- NULL sorts first (never yet served).
  last_served_at  TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE endpoints (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         UUID NOT NULL REFERENCES tenants(id),
  url               TEXT NOT NULL,
  event_types       TEXT[] NOT NULL,
  -- Unifies R-4 pause/resume and R-14 halt-at-ceiling as one concept rather
  -- than two independent flags.
  status            TEXT NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active', 'paused', 'halted')),
  -- Encrypted at rest (AES-256-GCM, internal/crypto), not plaintext
  -- (REVIEW.md F-16) — unlike the API key, this must be recoverable to
  -- compute HMACs, so it's encrypted rather than hashed. Decrypted only to
  -- sign a request or to return once on create/rotate.
  signing_secret    TEXT NOT NULL,
  -- docs/adr/0003: during a rotation overlap window, the sender signs with
  -- BOTH the current secret and this one — not receiver-only dual-check,
  -- which has a bootstrapping gap (the receiver can't check a secret it
  -- doesn't have yet). NULL outside of an active rotation window. Encrypted
  -- at rest, same as signing_secret.
  secondary_secret              TEXT,
  secondary_secret_expires_at   TIMESTAMPTZ,
  -- docs/adr/0002 ordering: one delivery in flight per endpoint at a time,
  -- enforced via a short-lived row lock on this row at claim time (not held
  -- for the outbound HTTP call). busy_since turns this into a lease — a
  -- claim older than the configured lease duration is reclaimable.
  busy              BOOLEAN NOT NULL DEFAULT false,
  busy_since        TIMESTAMPTZ,
  -- docs/adr/0002: fencing token set fresh at claim time, alongside
  -- busy/busy_since. Every post-HTTP-call write (attempt outcome, delivery
  -- state, busy release) must confirm lease_id still matches the value
  -- captured at claim before writing — a stalled (not dead) worker that wakes
  -- after its lease was reclaimed elsewhere has its write silently dropped
  -- instead of corrupting state a second worker already wrote.
  lease_id          UUID,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_endpoints_tenant ON endpoints (tenant_id, created_at);
-- Claim query: candidate endpoints ordered by tenant fairness, filtered to
-- ones that are actually claimable (not busy, or busy past the lease).
CREATE INDEX idx_endpoints_claimable ON endpoints (busy, busy_since) WHERE status = 'active';

CREATE TABLE events (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         UUID NOT NULL REFERENCES tenants(id),
  idempotency_key   TEXT NOT NULL,
  type              TEXT NOT NULL,
  payload           JSONB NOT NULL,
  -- docs/adr/0001 async expansion: R-9 (don't let a customer mistake zero
  -- deliveries-so-far for dropped) is satisfied by this explicit status, not
  -- by hoping expansion stays fast.
  status            TEXT NOT NULL DEFAULT 'pending_expansion'
                      CHECK (status IN ('pending_expansion', 'expanded')),
  -- docs/adr/0001: monotonic publish-order key. Expansion claims a tenant's
  -- oldest pending_expansion event by seq (not just created_at), serialized
  -- per tenant via pg_try_advisory_xact_lock(tenant_id) — this is what makes
  -- per-endpoint delivery order actually match publish order, since
  -- expansion itself is otherwise unordered across concurrent workers.
  seq               BIGSERIAL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX idx_events_pending_expansion_by_seq ON events (tenant_id, seq) WHERE status = 'pending_expansion';
CREATE INDEX idx_events_tenant_created ON events (tenant_id, created_at);

CREATE TABLE deliveries (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id          UUID NOT NULL REFERENCES events(id),
  endpoint_id       UUID NOT NULL REFERENCES endpoints(id),
  -- docs/adr/0002: ordering within an endpoint's queue follows this seq
  -- column, not created_at (docs/adr/0007) — Postgres's now() is
  -- transaction-stable, so a same-endpoint bulk insert (expansion, replay)
  -- would otherwise give every new row an identical created_at. This is why
  -- expansion and replay must insert rows in the order those events/
  -- deliveries originally happened: insertion order into this table *is*
  -- delivery order.
  seq               BIGSERIAL,
  -- 'failed' is terminal: attempt ceiling reached for this delivery, which is
  -- also what triggers endpoints.status = 'halted'. Halting itself is an
  -- endpoint-level concept — it doesn't get its own delivery state.
  state             TEXT NOT NULL DEFAULT 'pending'
                      CHECK (state IN ('pending', 'in_flight', 'succeeded', 'failed')),
  attempt_count     INT NOT NULL DEFAULT 0,
  next_attempt_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- "Blocked" (R-12/R-23) is computed at read time from an endpoint's oldest
-- unresolved delivery — this index is what makes that lookup cheap. Ordered
-- by seq, not created_at, for the same reason noted above.
CREATE INDEX idx_deliveries_endpoint_pending ON deliveries (endpoint_id, seq)
  WHERE state IN ('pending', 'in_flight');
CREATE INDEX idx_deliveries_event ON deliveries (event_id);
CREATE INDEX idx_deliveries_next_attempt ON deliveries (next_attempt_at) WHERE state = 'pending';

CREATE TABLE attempts (
  id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  delivery_id               UUID NOT NULL REFERENCES deliveries(id),
  attempt_number            INT NOT NULL,
  -- R-15: every attempt is recorded before the request is issued, with
  -- response status/body/duration/error class captured after.
  sent_at                   TIMESTAMPTZ,
  response_status           INT,
  response_body_truncated   TEXT,
  duration_ms               INT,
  -- docs/adr/0002: a reclaimed lease closes its orphaned attempt with
  -- error_class = 'worker_lease_expired' rather than leaving it dangling.
  error_class               TEXT,
  created_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_attempts_delivery ON attempts (delivery_id, attempt_number);

CREATE TABLE replays (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  endpoint_id       UUID NOT NULL REFERENCES endpoints(id),
  idempotency_key   TEXT NOT NULL,
  range_start       TIMESTAMPTZ NOT NULL,
  range_end         TIMESTAMPTZ NOT NULL,
  -- docs/adr/0005: replay is async-expanded exactly like events
  -- (docs/adr/0001) — the durable ack is this row alone; a worker later
  -- selects matching original deliveries (excluding still-pending/in_flight
  -- ones, which will be attempted on their own schedule regardless) and
  -- creates the replayed delivery rows in one atomic transaction, then flips
  -- this to 'expanded'. Replayed rows reuse the original event_id and land in
  -- the endpoint's normal queue (no new locking).
  status            TEXT NOT NULL DEFAULT 'pending_expansion'
                      CHECK (status IN ('pending_expansion', 'expanded')),
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (endpoint_id, idempotency_key)
);

CREATE INDEX idx_replays_pending_expansion ON replays (created_at) WHERE status = 'pending_expansion';
