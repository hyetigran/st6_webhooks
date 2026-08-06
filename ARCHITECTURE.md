# Architecture

Structural reference for the system. For *why* each piece looks this way — alternatives
considered, trade-offs accepted — see `DECISIONS.md`. For the original requirements, see
`PRD.md`. For the take-home brief this all serves, see `CASE_STUDY.md`.

## Overview

Two backend implementations — Node.js/TypeScript (`node/`) and Go (`go/`, not yet built) — are
architecturally identical: same schema, same concurrency mechanics, same REST contract. A single
shared frontend (`frontend/`, not yet built) can point at either one via a configurable base URL.
Neither backend uses an external message broker; PostgreSQL itself is the transactional queue.

```mermaid
graph LR
    SPA["Shared frontend<br/>(React SPA)<br/>polls every 2-5s"]

    subgraph Node["Node stack — node/"]
        direction TB
        NAPI["API process<br/>:3000"]
        NWorker["Worker process(es)"]
        NDB[("Postgres<br/>:5532, isolated")]
        NAPI <-->|"SQL"| NDB
        NWorker <-->|"claim: FOR UPDATE<br/>SKIP LOCKED"| NDB
    end

    subgraph Go["Go stack — go/ (planned)"]
        direction TB
        GAPI["API process"]
        GWorker["Worker process(es)"]
        GDB[("Postgres<br/>isolated, port TBD")]
        GAPI <-->|"SQL"| GDB
        GWorker <-->|"claim: FOR UPDATE<br/>SKIP LOCKED"| GDB
    end

    Recv["Customer's<br/>receiver endpoint"]

    SPA -->|"REST, identical contract<br/>Authorization: Bearer &lt;api_key&gt;"| NAPI
    SPA -.->|"base URL toggle"| GAPI
    NWorker -->|"HTTP POST, HMAC-signed<br/>Webhook-* headers"| Recv
    GWorker -.->|"HTTP POST, HMAC-signed"| Recv
```

Each stack's API process and worker process(es) are separate — publish latency stays
independent of delivery throughput (R-8). Each stack has its own isolated Postgres instance so
neither implementation's load or bugs can skew the other's measurements.

## Data model

Six tables (see `node/src/db/migrations/001_init.sql` for the canonical, commented definition
— the Go schema must match it). Fields shown are the ones that carry mechanism, not the full
column list:

```mermaid
erDiagram
    TENANTS ||--o{ ENDPOINTS : owns
    TENANTS ||--o{ EVENTS : publishes
    ENDPOINTS ||--o{ DELIVERIES : receives
    EVENTS ||--o{ DELIVERIES : "fans out to"
    DELIVERIES ||--o{ ATTEMPTS : "attempted via"
    ENDPOINTS ||--o{ REPLAYS : "replayed for"

    TENANTS {
        uuid id PK
        text api_key_hash UK "SHA-256, Bearer auth, F-16"
        timestamptz last_served_at "fairness ordering, ADR-007"
    }
    ENDPOINTS {
        uuid id PK
        uuid tenant_id FK
        text url
        text_array event_types
        text status "active | paused | halted"
        text signing_secret "AES-256-GCM encrypted, F-16"
        text secondary_secret "encrypted, rotation overlap, ADR-0003, nullable"
        timestamptz secondary_secret_expires_at "nullable"
        boolean busy "claim lock, ADR-002"
        timestamptz busy_since "lease clock, ADR-003"
        uuid lease_id "fencing token, ADR-0002"
    }
    EVENTS {
        uuid id PK
        uuid tenant_id FK
        text idempotency_key "composite-unique with tenant_id, R-6"
        text type
        jsonb payload
        text status "pending_expansion | expanded, ADR-004"
        bigint seq "publish-order key, ADR-0001"
    }
    DELIVERIES {
        uuid id PK
        uuid event_id FK
        uuid endpoint_id FK
        text state "pending|in_flight|succeeded|failed"
        int attempt_count
        timestamptz next_attempt_at "backoff schedule"
    }
    ATTEMPTS {
        uuid id PK
        uuid delivery_id FK
        int attempt_number
        int response_status
        int duration_ms
        text error_class "e.g. worker_lease_expired"
    }
    REPLAYS {
        uuid id PK
        uuid endpoint_id FK
        text idempotency_key "composite-unique with endpoint_id, R-21"
        timestamptz range_start
        timestamptz range_end
        text status "pending_expansion | expanded, ADR-0005"
    }
```

| Table | Purpose |
|---|---|
| `tenants` | One row per customer. `api_key_hash` (SHA-256, not plaintext) for auth, `last_served_at` for fairness ordering. |
| `endpoints` | A registered webhook target. `status` (active/paused/halted), `busy`/`busy_since`/`lease_id` (claim + fenced lease state), `signing_secret`/`secondary_secret` (rotation overlap). |
| `events` | A published event. `status` (pending_expansion/expanded), `idempotency_key`, `payload`. |
| `deliveries` | One row per (event, endpoint) pair. `state`, `attempt_count`, `next_attempt_at`. |
| `attempts` | One row per delivery attempt. Response status/body/duration/error, recorded before the request is sent. |
| `replays` | A replay request. `range_start`/`range_end`, `idempotency_key`. |

## Core mechanisms

- **Per-endpoint ordering**: a worker claims an endpoint's oldest pending delivery via a
  short-lived `FOR UPDATE SKIP LOCKED` on the endpoint row, sets `busy = true`, releases the
  lock, then makes the HTTP call outside any transaction. Order comes from the `deliveries`
  table's own insertion order — no separate sequence counter.
- **Crash recovery**: `busy_since` turns the claim into a lease. Reclaim is passive — folded
  into the same claim query (`busy = false OR busy_since` older than the lease duration) — no
  reaper process. Lease duration is derived from the outbound HTTP timeout, not an independent
  constant.
- **Async expansion**: publish inserts one `events` row and returns immediately. A shared
  worker pool later expands `pending_expansion` events into per-endpoint `deliveries` rows in
  one atomic transaction — claimed by `events.seq` under `pg_try_advisory_xact_lock(tenant_id)`,
  serializing expansion per tenant (not globally) so per-endpoint publish order survives
  concurrent workers (`docs/adr/0001`).
- **Tenant fairness**: the claim query orders candidates by `tenants.last_served_at ASC` first,
  endpoint's oldest pending delivery second — one rule gives both no-overconsumption and
  no-starvation. Bounded, not just assumed: a quiet tenant's added latency under a slow-receiver
  attack is roughly one outbound-timeout cycle, since the rule routes the next claim to the
  longest-unserved tenant as soon as any worker frees (`docs/adr/0004`). Accepted: every claim
  updates `tenants.last_served_at`, so a busy tenant's endpoints serialize claims on one hot
  row — fine at this project's target scale, a real scaling concern only past it. **Lock
  ordering invariant** (deadlock-free by construction, but only if preserved): a claim
  transaction always locks the endpoint row first (the claim query's `FOR UPDATE OF e`), then
  updates the tenant row second. Any future code that touches both an endpoint and its tenant
  row in one transaction must acquire them in that same order.
- **Retry & halt**: full-jitter exponential backoff (1s base, 2x, 30s cap, 6 attempts). At the
  ceiling, the delivery's `state` becomes `failed` and its `endpoints.status` becomes `halted`;
  deliveries are retained, not discarded.
- **Replay**: reuses the same per-endpoint queue and claim mechanism — a replay just inserts new
  `deliveries` rows (fresh `delivery_id`, same `event_id`) in original chronological order.
- **Receiver contract**: outbound requests are signed with HMAC-SHA256 over
  `"{timestamp}.{raw_body}"`, carried in `Webhook-*` headers (no legacy `X-` prefix). During a
  rotation overlap window, the request is signed once per active secret (current + secondary)
  so a receiver on either one still verifies (`docs/adr/0003`). The connection itself
  resolve-validate-pins the endpoint's IP (not a separate validate-then-connect) and follows no
  redirects, closing the DNS-rebinding and redirect-to-private-address gaps a bounded-hop-count
  policy alone doesn't (`docs/adr/0006`).

## Request flows

Each diagram labels the concrete payload at every handoff — not just "A calls B" but what A
hands B and what B hands back, since that's usually where an integration actually breaks.

### Publish → expand → deliver

The core path. Two separate worker-pool loops are shown — expansion and delivery — because
they're different transactions against different tables, even though the same physical worker
process runs both.

```mermaid
sequenceDiagram
    autonumber
    participant Pub as Publisher<br/>(internal service)
    participant API as API process
    participant DB as Postgres
    participant Worker as Worker process
    participant Recv as Customer receiver

    Pub->>API: POST /events<br/>Idempotency-Key: (key)<br/>{type, payload}
    API->>DB: INSERT events<br/>(status='pending_expansion')<br/>ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
    alt row inserted
    DB-->>API: new event row {id, status}
    else conflict — DO NOTHING returns zero rows
    API->>DB: SELECT id, status FROM events<br/>WHERE (tenant_id, idempotency_key)
    DB-->>API: original event row {id, status}<br/>— R-6 requires the original id, not a fresh one
    end
    API-->>Pub: 202 {id, status}

    rect rgba(100,100,200,0.08)
    note right of Worker: expansion loop — serialized per tenant (ADR-0001)
    Worker->>DB: pg_try_advisory_xact_lock(tenant_id)<br/>— skip this tenant if already held elsewhere
    DB-->>Worker: lock acquired
    Worker->>DB: SELECT oldest pending_expansion event<br/>WHERE tenant_id ORDER BY seq LIMIT 1
    DB-->>Worker: event row (publish-order guaranteed within this tenant)
    Worker->>DB: SELECT endpoints<br/>WHERE tenant_id AND event_types @> event.type
    DB-->>Worker: subscribed endpoints (snapshot at this instant)
    Worker->>DB: INSERT deliveries (one row/endpoint)<br/>+ UPDATE events SET status='expanded'<br/>— one transaction, advisory lock releases on commit
    end

    rect rgba(100,200,100,0.08)
    note right of Worker: delivery loop
    Worker->>DB: claim: FOR UPDATE SKIP LOCKED<br/>ORDER BY tenants.last_served_at, endpoint's oldest pending
    DB-->>Worker: endpoint row (locked)
    Worker->>DB: SET busy=true, busy_since=now()<br/>UPDATE tenants.last_served_at=now()<br/>— commit, lock released
    Worker->>DB: INSERT attempts (attempt_number, sent_at)<br/>— recorded before the request, per R-15
    Worker->>Recv: POST {payload}<br/>Webhook-Id, Webhook-Event-Id, Webhook-Attempt,<br/>Webhook-Timestamp, Webhook-Signature
    Recv-->>Worker: 2xx | non-2xx | timeout
    Worker->>DB: UPDATE attempts (response_status, duration_ms, error_class)<br/>UPDATE deliveries.state<br/>SET endpoints.busy=false
    end
```

### Crash recovery: lease reclaim (dead worker)

Demonstrated by `make chaos` — kill a worker mid-delivery, confirm no event is lost and the
endpoint doesn't stay stuck. A killed process never reaches a write-back, so this case needs
no fencing — it's the *stall* case below that does.

```mermaid
sequenceDiagram
    autonumber
    participant WA as Worker A
    participant DB as Postgres
    participant Recv as Customer receiver
    participant WB as Worker B

    WA->>DB: claim endpoint (busy=false)<br/>SET busy=true, busy_since=now(), lease_id=uuid_A
    WA->>DB: INSERT attempts (in-flight, no response yet)
    WA->>Recv: POST {payload}
    note over WA: 💥 process dies —<br/>no response ever recorded, no write-back attempted

    note over DB: endpoint stuck busy=true<br/>until lease_duration elapses<br/>(= outbound_timeout + max(outbound_timeout, 30s))

    WB->>DB: claim query:<br/>busy=false OR busy_since older than lease_duration
    DB-->>WB: endpoint row (stale lease, reclaimed — same query, no reaper)<br/>SET lease_id=uuid_B
    WB->>DB: UPDATE attempts SET error_class='worker_lease_expired'<br/>(closes the orphaned attempt, keeps R-15's history honest)
    WB->>DB: INSERT attempts (fresh attempt_number)<br/>SET busy_since=now()
    WB->>Recv: POST {payload}<br/>(retry — may look like a duplicate to the receiver, at-least-once is expected here)
    Recv-->>WB: 2xx
    WB->>DB: fenced write-back: confirm lease_id=uuid_B, then<br/>UPDATE attempts (success), deliveries.state='succeeded', endpoints.busy=false
```

### Crash recovery: fencing a stalled (not dead) worker

The bug fencing actually fixes — a killed process can't corrupt anything, but a *stalled* one
(GC pause, container CPU throttling, `SIGSTOP`) can wake up after losing its lease and still
try to write.

```mermaid
sequenceDiagram
    autonumber
    participant WA as Worker A
    participant DB as Postgres
    participant Recv as Customer receiver
    participant WB as Worker B

    WA->>DB: claim endpoint<br/>SET busy=true, busy_since=now(), lease_id=uuid_A
    WA->>Recv: POST {payload}
    note over WA: ⏸ process stalls (GC / CPU throttle / SIGSTOP)<br/>— request still outstanding, A's own timers don't fire either

    note over DB: lease_duration elapses while A is stalled
    WB->>DB: claim query reclaims<br/>SET lease_id=uuid_B
    WB->>Recv: POST {payload}
    Recv-->>WB: 2xx
    WB->>DB: fenced write-back: confirm lease_id=uuid_B — matches, proceeds<br/>UPDATE attempts, deliveries.state='succeeded', endpoints.busy=false

    note over WA: ▶ A resumes — its original request finally resolves
    Recv-->>WA: 2xx (A's own response, arrives late)
    WA->>DB: attempted write-back: confirm lease_id=uuid_A — no longer matches (now uuid_B)<br/>write dropped silently, no state overwritten
```

### Replay

Async-expanded exactly like publish (`docs/adr/0005`) — the durable ack is the single `replays`
insert, not the window query or the delivery inserts, which run later in the worker pool.

```mermaid
sequenceDiagram
    autonumber
    participant Cust as Customer<br/>(via UI)
    participant API as API process
    participant DB as Postgres
    participant Worker as Worker process

    Cust->>API: POST /endpoints/{id}/replays<br/>Idempotency-Key: (key)<br/>{range_start, range_end}
    API->>DB: INSERT replays (status='pending_expansion')<br/>ON CONFLICT (endpoint_id, idempotency_key) DO NOTHING
    alt row inserted
    DB-->>API: new replay row {id, status}
    else conflict — DO NOTHING returns zero rows
    API->>DB: SELECT id, status FROM replays<br/>WHERE (endpoint_id, idempotency_key)
    DB-->>API: original replay row {id, status}
    end
    API-->>Cust: 202 {id, status}

    rect rgba(100,100,200,0.08)
    note right of Worker: replay expansion loop — mirrors event expansion
    Worker->>DB: claim: FOR UPDATE SKIP LOCKED<br/>WHERE status='pending_expansion'
    DB-->>Worker: replay row (locked)
    Worker->>DB: SELECT deliveries WHERE endpoint_id<br/>AND created_at BETWEEN range_start AND range_end<br/>AND state NOT IN ('pending','in_flight')<br/>ORDER BY created_at — any resolved outcome, excludes not-yet-attempted
    DB-->>Worker: matching original deliveries
    Worker->>DB: INSERT deliveries (fresh delivery_id, same event_id)<br/>+ UPDATE replays SET status='expanded'<br/>— one transaction, same relative order as originals
    end

    note over Worker,DB: new rows enter the endpoint's normal queue —<br/>no new locking, the delivery loop above already applies.<br/>A large replay can delay live deliveries on the SAME endpoint,<br/>by design (R-22) — other endpoints are unaffected.
    Worker->>DB: normal claim query<br/>(replayed + live deliveries interleaved FIFO by insertion order)
```

## State model

### Endpoint status

Only `active` endpoints are claimable — the delivery worker's claim query filters
`status = 'active'` (matched by `idx_endpoints_claimable`'s partial index), which is what makes
R-4's "paused endpoints accumulate deliveries rather than discarding them" actually hold: a
`paused` endpoint's deliveries just sit `pending`, never claimed, until it's active again.

`resume` states its ordering consequence — `skipped_failed_delivery_ids` plus
`pending_delivery_count` — **every time it's called, from either prior status**, not just on
the direct `halted → active` edge. Reaching `active` via `halted → paused → active` gets the
identical disclosure, since the check is keyed on delivery state, not on which status the
endpoint is resuming from (`REVIEW.md` F-12; verified live).

```mermaid
stateDiagram-v2
    [*] --> active: POST /endpoints
    active --> paused: POST /pause
    paused --> active: POST /resume<br/>(skipped_failed_delivery_ids + pending_delivery_count, always)
    active --> halted: a delivery's attempt_count<br/>reaches the ceiling (R-14)
    halted --> active: POST /resume<br/>(same disclosure as above)
    halted --> paused: POST /pause
```

### Delivery state

```mermaid
stateDiagram-v2
    [*] --> pending: row created by expansion or replay
    pending --> in_flight: claimed by a worker
    in_flight --> succeeded: 2xx response
    in_flight --> pending: non-2xx / timeout<br/>(backoff scheduled, next_attempt_at set)
    in_flight --> pending: worker lease expired<br/>(passive reclaim, no data lost)
    pending --> failed: attempt ceiling reached<br/>(endpoint's status becomes halted too)
    succeeded --> [*]
    failed --> [*]
```

`Blocked` (R-12/R-23) isn't a stored state — it's computed at read time as "this delivery isn't
the endpoint's current head, and the head hasn't resolved yet." See `DECISIONS.md` for why.

## Directory layout

```
/
├── CASE_STUDY.md      the assessment brief (pre-existing)
├── PRD.md             product requirements (pre-existing)
├── DECISIONS.md        architecture decisions, ~2 pages, submission format
├── ARCHITECTURE.md     this file
├── DELIVERABLES.md     what must ship and current status
├── memory-bank/        persistent context for AI sessions — read first
├── node/               Node.js/TypeScript backend (in progress)
├── go/                 Go backend (not yet started)
└── frontend/           shared React SPA (not yet started)
```

## Where the live status lives

This file describes the target shape. For what's actually built right now, see
`DELIVERABLES.md` and the wayfinder map (GitLab issue #1 in this project) — the map is the
source of truth for ticket-level progress; this file is a structural reference that shouldn't
need to change unless the architecture itself changes.
