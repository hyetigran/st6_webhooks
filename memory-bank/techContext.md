# Tech Context

Concrete stack, tooling, and environment facts. Update this when a real setup fact changes —
not aspirational, only what's actually true right now.

## Node stack (`node/`) — built

- **Language/runtime**: TypeScript, Node.js >=20, ESM (`"type": "module"`).
- **Framework**: Express 4. Async route handlers must be wrapped in
  `src/lib/asyncHandler.ts` — Express 4 doesn't forward rejected promises to error middleware
  on its own; without the wrapper a thrown error hangs the request instead of 500ing.
- **DB driver**: `pg` (node-postgres), plain SQL (no ORM), migrations are hand-written `.sql`
  files applied by `src/db/migrate.ts`.
- **Validation**: `zod`.
- **Dev loop**: `npm run dev` (tsx watch), `npm run migrate`, `npm run seed`, `npm run
  typecheck`.
- **Local Postgres port**: **5532**, not 5433 — 5433 was already bound by an unrelated project
  on the dev machine. `DATABASE_URL` default and `docker-compose.yml` both use 5532.
- **API port**: 3000.
- **Auth**: tenants are seeded out-of-band via `npm run seed` (prints an API key) — there is no
  signup route, deliberately.

## Go stack (`go/`) — not started

No decisions made yet beyond what's in `DECISIONS.md` (same schema, same mechanisms, same REST
contract as Node). When scaffolding starts: **avoid port 5532** (Node's) and avoid re-colliding
with whatever else is running locally — check with `lsof -nP -iTCP:<port> -sTCP:LISTEN` before
picking a port, the same way Node's setup discovered the 5433 conflict. A reasonable next choice
is 5533 for Postgres, 8080 for the API, but verify against what's actually free at build time.

## Frontend (`frontend/`) — not started

React SPA per `DECISIONS.md`'s ADR-008. Base URL configurable to point at either backend.
Polling every 2-5s, no WebSocket/SSE. No framework choice recorded yet beyond "React."

## Config conventions (apply to both backends)

Every tunable is an env var with a sensible default, not a hardcoded constant — this is so the
test suite can override defaults for fast, deterministic runs without picking artificially small
production values. See `node/.env.example` for the full list: backoff params, outbound HTTP
timeouts, signature timestamp tolerance, secret rotation overlap window.

## Ports in use on the dev machine (informational, may drift)

Checked once during Node setup — other local projects were occupying 5433, 54321-54327, 8300,
8310, 8320, 5984, 6984, 4444, 7900, 1025, 8025. Re-check with `lsof` rather than trusting this
list if a "port already allocated" error shows up again.
