# Progress

What works, what doesn't exist yet, and known issues. For live ticket-level status, the
wayfinder map (GitLab issue #1) is authoritative — this file is a snapshot for fast orientation,
not a replacement for it.

## What works

- **Node — endpoint management API** (`node/`): schema migrated (post-review — see below),
  Bearer auth (API key hashed, SHA-256), R-2 URL validation (IPv4/IPv6, literal + DNS-resolved
  private/loopback/link-local/CGNAT rejection), all six endpoint routes (register/list/detail/
  pause/resume/rotate-secret). Signing secrets encrypted at rest (AES-256-GCM). `resume`
  discloses `skipped_failed_delivery_ids` alongside `pending_delivery_count`, computed
  identically regardless of prior status. Verified live against a real Postgres instance
  multiple times, including after the credential-handling and resume fixes — see `#16`'s
  resolution comment and `REVIEW.md`'s F-3/F-12/F-16 resolution notes for test transcripts.
- **Node — publish & async expansion** (`#17`): `POST /events` with `Idempotency-Key`
  idempotency (ON CONFLICT + fallback SELECT), and the worker's expansion cycle using
  `docs/adr/0001`'s per-tenant advisory-lock serialization (`events.seq` order, not naive
  parallel claiming). `node/src/worker.ts` poll-loop entrypoint. First test infrastructure in
  the project (vitest + dedicated `webhooks_node_test` database).
- **Node — delivery worker** (`#18`): the full claim → sign → send → write-back cycle.
  Tenant-fairness-ordered claim with endpoint-level `busy`/`busy_since`/`lease_id`
  (`docs/adr/0002`/`0004`), SSRF-safe resolve-validate-pin HTTP client with pinned connections
  and no redirect-following (`docs/adr/0006`), multi-secret HMAC signing during rotation
  overlap (`docs/adr/0003`), full-jitter backoff with attempt-ceiling halt, passive lease
  reclaim with a synthetic `worker_lease_expired` outcome on orphaned attempts (no reaper
  process). `worker.ts` now runs expansion and delivery in one shared poll loop. **Live-verified
  end to end against a real public receiver** (httpbin.org) through the actual running worker —
  not a mock, the real registration SSRF check, real DNS resolution, real HMAC signature
  confirmed via the receiver's own echo response.
  - This closes the five "Design fixed" `REVIEW.md` findings (F-1, F-2, F-5, F-8, F-10) that
    were waiting on this worker to exist as real, tested code.
- **Node — replay** (`#19`): `POST /endpoints/:id/replays` mirrors publish's async two-phase
  shape (`docs/adr/0005`) — 202 immediately, a worker cycle later expands. The replay-expansion
  cycle excludes still-pending/in_flight originals, creates fresh delivery rows reusing the
  original `event_id`, and reuses the delivery worker's existing claim mechanism with no new
  locking. **Found and fixed a real ordering bug while implementing**: added `deliveries.seq
  BIGSERIAL` (`docs/adr/0007`) since replay's bulk same-endpoint insert would otherwise tie on
  Postgres's transaction-stable `now()` — see the gotchas section below. Live-verified end to
  end: replayed a real previously-delivered event against httpbin.org through the actual
  running worker.
- **Node — visibility & read API** (`#20`): `GET /events` (search by type/endpoint_id/from/to/
  id, R-24), `GET /events/:id` (fan-out summary), `GET /deliveries/:id` (state/attempt_count/
  next_attempt_at/`blocked_on_delivery_id` per R-23, `last_response`, attempts capped at 6),
  `GET /endpoints/:id/deliveries` (queue view, ascending by `seq`, its own `after`-cursor).
  `blocked_on_delivery_id` computed read-time from each endpoint's head, shared via
  `node/src/lib/deliveryQueries.ts`. R-25's health fields were already done in `#16`.
  Live-verified against real historical delivery data through the running API.
- **Full documentation set, adversarially reviewed**: `ARCHITECTURE.md`, `DECISIONS.md`,
  `CONTEXT.md`, `COMPARISON.md`, `PRD.md` all went through a 17-finding review (`REVIEW.md`) and
  came out corrected — this isn't just "written," it's been checked for internal consistency,
  arithmetic correctness, and real design bugs. 6 ADRs in `docs/adr/` record the corrections.
- **All 8 Mermaid diagrams in `ARCHITECTURE.md`** validated against the real parser (not just
  visual inspection) — publish/expand/deliver, crash recovery (dead worker + stalled worker,
  two separate diagrams), replay, both state diagrams, the system overview, and the ER diagram.

## What's built but not yet exercised end-to-end

Nothing currently — everything built so far, including `#18`'s delivery worker and `#20`'s read
routes, has been verified live against a real running process and (for `#18`/`#19`/`#20`) real
external infrastructure or real historical data.

## What doesn't exist yet

- **Node**: test suite + deployment docs (`#21`, the last Node ticket — owes the closing tests
  for 5 "Design fixed" review findings: F-1, F-2, F-5, F-8, F-10, now implementable since `#18`
  exists; also owes `docs/adr/0004`'s tarpit-tenant fairness `make load` scenario, and real
  multi-worker same-endpoint concurrency races, all explicitly deferred from `#17`/`#18`/`#19`).
  Also: `replays.status` was never added to the REST contract for polling replay progress
  (`docs/adr/0005`'s "Consequences" note flagged this as not-yet-done) — still open.
- **Go**: the entire stack (`#22-27`), mirroring Node ticket-for-ticket — including every
  review-driven fix, not the pre-review design. The Go schema must match Node's *current*
  schema exactly, which as of `#19` is `001_init.sql` **plus** `002_deliveries_seq.sql`
  (`deliveries.seq`) — not `001_init.sql` alone.
- **Frontend**: entire SPA (`#28-30`) — buildable now against the fixed REST contract.
- **`README.md`**: the *final submission* version is still not started for either stack (part of
  `#21`/`#27`). The current root `README.md` is an interim orientation doc, explicitly marked as
  such, and now also carries the `reqs not read` omission note (removed a second time by the
  user directly, confirmed intentional — see `activeContext.md`) and the AI-usage/transcripts
  disclosure.
- **Primary-build designation**: can't be decided until both stacks have a working `make load`
  test to measure (see `DECISIONS.md`, "Submission").
- **C-2 (time spent) in `REVIEW.md`'s checklist**: still needs the user's answer.

## Known issues / gotchas for future sessions

- **Port 5433 was already taken** by an unrelated local project when Node's Postgres was set up
  — Node uses **5532** instead. Check port availability with `lsof` before assuming a "standard"
  port is free; this will likely recur when Go's docker-compose is created.
- **Express 4 async error handling**: routes must use `src/lib/asyncHandler.ts` or a thrown
  error in an async handler hangs the request instead of producing a 500.
- **`SECRET_ENCRYPTION_KEY` is required, no fallback** (`node/src/config.ts`) — must be a
  base64-encoded 32-byte key (`openssl rand -base64 32`); the app throws on startup if missing
  or the wrong length. `.env.example` has a working local-dev key; generate a real one for
  anything beyond that.
- **Mermaid's sequence-diagram grammar breaks on `&lt;`/`&gt;` HTML entities and mid-message
  semicolons** — discovered the hard way validating `ARCHITECTURE.md`'s diagrams against the
  real parser. Use literal `<text>` or parentheses, and avoid semicolons inside a message; a
  trailing semicolon at end-of-line is fine.
- **Postgres's `now()` is transaction-stable, not statement-stable** — every statement inside
  one transaction sees the identical value (unlike `clock_timestamp()`, which advances per
  call). A multi-row same-endpoint `INSERT ... SELECT` relying on a `created_at DEFAULT now()`
  column to preserve relative insertion order will silently break: every new row gets the exact
  same timestamp, and ties are broken arbitrarily wherever they're later sorted on. `#19`'s replay-expansion cycle hit exactly this; fixed with a dedicated `seq BIGSERIAL` column
  (`deliveries.seq`, `docs/adr/0007`), mirroring `events.seq`'s existing fix for the same class
  of problem. **The Go implementation needs the identical fix** — this isn't Node-specific, it's
  a Postgres semantic, and Go's driver will hit the exact same bug if a bulk same-endpoint
  insert relies on `created_at` for order.
- **Never compare a Node-side `new Date()` timestamp against Postgres's own `now()` in a query**
  — real clock skew between the Node process and the Docker Postgres container caused
  intermittent test failures in `#18` (`test/fixtures.ts`'s `createPendingDelivery`). Let
  Postgres compute its own timestamp (`now()` in the SQL) whenever a value will later be
  compared server-side against `now()`, rather than passing one in as a parameter. Production
  code was already following this correctly (expansion's `DEFAULT now()`, `completeDelivery`'s
  retry scheduling) — this was a test-fixture-only bug.
- **Node's custom `http.request`/`https.request` `lookup` option must handle the array-form
  callback** — since Node 18 (Happy Eyeballs), `net` calls a custom `lookup` with `{ all: true }`
  and expects `callback(null, [{address, family}])`, not the classic `callback(null, address,
  family)`. `node/src/worker/httpClient.ts`'s `pinnedLookup` handles both forms; easy to miss
  and get a confusing `ERR_INVALID_IP_ADDRESS` instead.
- **`npm audit` flags 5 vulnerabilities** in `node/`, all transitive dev-dependencies of
  `vitest`'s bundled `esbuild` dev server (not production code, not shipped). Not worth fixing
  unless it starts blocking something.
- **`GET /endpoints/:id/deliveries` deliberately breaks the API's own pagination convention**:
  every other list route uses `?before=<created_at+id cursor>`, newest-first. This one route
  (`#20`) uses `?after=<seq cursor>`, ascending (head-first) — a queue view's natural read order
  is the opposite of a list view's. The Go implementation must match this exact deviation, not
  "fix" it toward consistency, or the shared frontend (ADR-008) breaks against one backend.
