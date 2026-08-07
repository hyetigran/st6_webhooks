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
  signup route, deliberately. The key is hashed (SHA-256) before storage, in `tenants.api_key_hash`.
- **Crypto**: `src/lib/crypto.ts` — `hashApiKey` (SHA-256, for auth lookups) and
  `encryptSecret`/`decryptSecret` (AES-256-GCM, for `signing_secret`/`secondary_secret` at rest).
  Requires `SECRET_ENCRYPTION_KEY` (32-byte, base64) in env — no fallback, throws on startup if
  missing or wrong length.

## Go stack (`go/`) — schema/scaffolding/endpoint-management API built (`#22`)

- **Language/runtime**: Go 1.26 (installed via Homebrew — wasn't present on the dev machine when
  `#22` started).
- **HTTP**: stdlib `net/http`, Go 1.22+'s method+`{wildcard}` `ServeMux` — no framework
  (confirmed with the user over `chi`).
- **DB driver**: `pgx`/`pgxpool` — no ORM, plain SQL, same posture as Node's `pg` (confirmed with
  the user over `lib/pq`+`database/sql`).
- **Migrations**: hand-written `.sql` files under `internal/db/migrations/`, embedded into the
  binary via `go:embed` (`internal/db/migrate.go`) and applied by `cmd/migrate` — unlike Node's
  Dockerfile, no separate "copy migrations into the image" step is needed.
- **Local Postgres port**: **5533** — Node's 5532, and 5432/5433, were already taken.
- **API port**: **8090** — Node's 3000 and local ports 8080-8083 were already taken.
- **Auth**: same as Node — tenants seeded out-of-band via `go run ./cmd/seed`, no signup route.
- **Crypto**: `internal/crypto` — `HashAPIKey` (SHA-256) and `EncryptSecret`/`DecryptSecret`
  (AES-256-GCM, same `iv || authTag || ciphertext` base64 layout as Node's). Key passed directly
  into `api.NewServer` (via `config.SecretEncryptionKey()`, required, no fallback) rather than
  read from a package-level global — cleaner dependency injection than Node's approach.
- **Not yet built**: `go/cmd/worker/` doesn't exist yet (`#23` adds it), nor do `#23`-`#27`'s
  mechanisms (publish/expansion, delivery, replay, visibility, PRD §8 test suite).

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
