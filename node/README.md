# Webhook delivery service — Node.js/TypeScript

A reliable webhook delivery system: register endpoints, publish events, get durable
at-least-once delivery with strict per-endpoint ordering, retries with backoff, replay, and a
full read API for answering "what happened to this event" without reading logs.

This is one of two architecturally-identical implementations built for `CASE_STUDY.md`'s
Project 1 — see the repo root's `DECISIONS.md` (why things are built this way) and
`ARCHITECTURE.md` (component/data-model reference) for the full design. This file is just
"clone it, run it, understand it" — everything else lives at the root.

## Prerequisites

- Node.js 20+
- Docker (for Postgres, and optionally for running the full stack)

## Quickstart

```sh
cd node
docker compose up -d postgres
cp .env.example .env
npm install
npm run migrate
npm run seed            # prints a demo tenant's API key — save it
npm run dev              # API on :3000
npm run dev:worker       # in a second terminal — expansion/delivery/replay worker
```

`GET /healthz` is unauthenticated. Every other route needs `Authorization: Bearer <api_key>`
from the seed step above.

## Try it

Using the API key from `npm run seed`:

```sh
API_KEY=tenant_xxxxx  # from the seed step

# Register an endpoint. The URL must resolve to a real, non-private address —
# registration performs the same SSRF-safe DNS check delivery does (R-2/ADR-0006).
curl -s -X POST http://localhost:3000/endpoints \
  -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
  -d '{"url":"https://httpbin.org/post","event_types":["order.created"]}'
# => {"id":"...", "signing_secret":"whsec_...", ...}  — the secret is shown once (R-3)

# Publish an event. Idempotency-Key is a header, not a body field (R-6).
curl -s -X POST http://localhost:3000/events \
  -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
  -H "Idempotency-Key: order-123" \
  -d '{"type":"order.created","payload":{"orderId":"abc123"}}'
# => {"id":"...", "status":"pending_expansion"} (202) — the worker expands and delivers async

# Check what happened (poll — this is a UI-visibility API, not a webhook back to you):
curl -s http://localhost:3000/deliveries/<delivery_id> -H "Authorization: Bearer $API_KEY"
```

## Verifying a received webhook (receiver contract)

Every delivery carries five headers, no `X-` prefix (PRD §6):

| Header | Meaning |
| --- | --- |
| `Webhook-Id` | Unique per delivery attempt sequence |
| `Webhook-Event-Id` | Stable across retries and replays — dedupe on this |
| `Webhook-Attempt` | 1-indexed attempt number |
| `Webhook-Timestamp` | Unix seconds |
| `Webhook-Signature` | One HMAC-SHA256 hex digest per still-active secret, comma-joined |

```js
const crypto = require("node:crypto");

function verify(rawBody, timestamp, signatureHeader, secret) {
  const expected = crypto.createHmac("sha256", secret).update(`${timestamp}.${rawBody}`).digest("hex");
  // ADR-0003: during a rotation overlap window, the sender signs with every
  // still-active secret — check against all of them, not just the current one.
  return signatureHeader.split(",").includes(expected);
}
```

Reject requests where `|now - timestamp| > 300s` (replay protection). Dedupe on
`Webhook-Event-Id` — **only after your handler has successfully processed it**, not merely
received it (PRD §6: deduping at receipt time would silently no-op the replay of an event that
never actually completed).

## Running the test suite

```sh
npm test                 # full vitest suite — unit + integration against a real Postgres
npm run typecheck
```

For PRD.md §8's full acceptance criteria (chaos, property, and load tests — these spawn real
child processes and take longer):

```sh
make test          # same as npm test
make properties     # seeded, randomized invariants — logs its seed for reproduction
make chaos          # real process kill/SIGSTOP/SIGCONT against a real worker pool, ~10s
make load            # real HTTP latency against a real api/worker pool, ~10s
make verify          # everything above — writes evidence to ../evidence/{chaos,load}/
```

`make chaos`/`make load` each manage their own dedicated database
(`webhooks_node_chaos`/`webhooks_node_load`), separate from your dev data and the vitest test
DB — safe to run alongside `npm run dev`.

## Running the full stack in Docker

```sh
docker compose up -d --build
```

Brings up Postgres, the API, and the worker, each running migrations on startup. Same
`Authorization`/`Idempotency-Key` contract as above, on `:3000`. Seed a tenant from inside the
container: `docker compose exec api node dist/db/seed.js <name>`.

## Project structure

```
src/
  routes/       endpoints, events (publish + search), deliveries, replays
  worker/       expansion, delivery, replayExpansion — the shared worker pool's three cycles
  worker.ts     the poll-loop entrypoint (runs all three cycles in one process)
  lib/          crypto, signing, pagination, secrets, asyncHandler
  validation/   SSRF-safe URL validation (shared by registration and delivery)
  db/           migrations, pool, migrate/seed scripts
test/           vitest suite, including test/properties/ (seeded invariants)
chaos/          make chaos scenario scripts + shared harness
load/           make load scenario scripts + shared harness
```

## Known gotchas

- **Postgres runs on host port `5532`, not the Postgres default `5432`** — an unrelated local
  project was already using 5432/5433 during development. Check `docker-compose.yml` if a port
  conflict comes up.
- **`SECRET_ENCRYPTION_KEY` is required, no fallback.** Must be a base64-encoded 32-byte key
  (`openssl rand -base64 32`). The app throws on startup if it's missing or the wrong length.
  `.env.example` ships a working local-dev key — generate your own for anything beyond that.
- **Endpoint URLs must resolve to a real, non-private address**, at both registration and
  delivery time (R-2/R-16, ADR-0006) — `http://localhost/...` or any RFC1918 address will be
  rejected. This is a deliberate SSRF defense, not a bug; use a real reachable URL (a tool like
  `httpbin.org/post`, or your own publicly-reachable dev tunnel) to try delivery locally.
