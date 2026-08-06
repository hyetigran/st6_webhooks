# Architecture

Structural reference for the system. For *why* each piece looks this way — alternatives
considered, trade-offs accepted — see `DECISIONS.md`. For the original requirements, see
`PRD.md`. For the take-home brief this all serves, see `CASE_STUDY.md`.

## Overview

Two backend implementations — Node.js/TypeScript (`node/`) and Go (`go/`, not yet built) — are
architecturally identical: same schema, same concurrency mechanics, same REST contract. A single
shared frontend (`frontend/`, not yet built) can point at either one via a configurable base URL.
Neither backend uses an external message broker; PostgreSQL itself is the transactional queue.

```
                 ┌──────────────┐
                 │  Frontend    │  React SPA, polls every 2-5s
                 │  (shared)    │  points at either backend via base URL
                 └──────┬───────┘
                        │ REST (identical contract on both backends)
           ┌────────────┴────────────┐
           │                         │
   ┌───────▼────────┐        ┌───────▼────────┐
   │  Node API proc  │        │   Go API proc  │
   └───────┬────────┘        └───────┬────────┘
           │                         │
   ┌───────▼────────┐        ┌───────▼────────┐
   │ Node worker(s)  │        │  Go worker(s)  │
   └───────┬────────┘        └───────┬────────┘
           │                         │
   ┌───────▼────────┐        ┌───────▼────────┐
   │ Postgres (node) │        │ Postgres (go)  │   isolated per stack
   └────────────────┘        └────────────────┘
```

Each stack's API process and worker process(es) are separate — publish latency stays
independent of delivery throughput (R-8). Each stack has its own isolated Postgres instance so
neither implementation's load or bugs can skew the other's measurements.

## Data model

Six tables (see `node/src/db/migrations/001_init.sql` for the canonical, commented definition
— the Go schema must match it):

| Table | Purpose |
|---|---|
| `tenants` | One row per customer. `api_key` for auth, `last_served_at` for fairness ordering. |
| `endpoints` | A registered webhook target. `status` (active/paused/halted), `busy`/`busy_since` (claim + lease state), `signing_secret`. |
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
  one atomic transaction.
- **Tenant fairness**: the claim query orders candidates by `tenants.last_served_at ASC` first,
  endpoint's oldest pending delivery second — one rule gives both no-overconsumption and
  no-starvation.
- **Retry & halt**: full-jitter exponential backoff (1s base, 2x, 30s cap, 6 attempts). At the
  ceiling, `endpoints.status` becomes `halted`; deliveries are retained, not discarded.
- **Replay**: reuses the same per-endpoint queue and claim mechanism — a replay just inserts new
  `deliveries` rows (fresh `delivery_id`, same `event_id`) in original chronological order.
- **Receiver contract**: outbound requests are signed with HMAC-SHA256 over
  `"{timestamp}.{raw_body}"`, carried in `Webhook-*` headers (no legacy `X-` prefix).

## Request flows

**Publish → deliver**: `POST /events` → insert `events` row (`pending_expansion`) → 202
returned → worker pool expands into `deliveries` rows → worker pool claims and delivers each,
retrying with backoff until success or halt.

**Replay**: `POST /endpoints/{id}/replays` → select matching original deliveries in the range →
insert new `deliveries` rows referencing the same `event_id` → drains through the endpoint's
normal queue alongside (and, by design, sometimes behind) live traffic.

**Visibility**: every read (`GET /events`, `GET /deliveries/{id}`, `GET /endpoints`, etc.) queries
the same primary Postgres directly — no read replica, no cache, no separate read model.

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
