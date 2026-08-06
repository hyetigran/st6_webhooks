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
- **Full documentation set, adversarially reviewed**: `ARCHITECTURE.md`, `DECISIONS.md`,
  `CONTEXT.md`, `COMPARISON.md`, `PRD.md` all went through a 17-finding review (`REVIEW.md`) and
  came out corrected — this isn't just "written," it's been checked for internal consistency,
  arithmetic correctness, and real design bugs. 6 ADRs in `docs/adr/` record the corrections.
- **All 8 Mermaid diagrams in `ARCHITECTURE.md`** validated against the real parser (not just
  visual inspection) — publish/expand/deliver, crash recovery (dead worker + stalled worker,
  two separate diagrams), replay, both state diagrams, the system overview, and the ER diagram.

## What's built but not yet exercised end-to-end

Nothing currently — everything built so far, including `#18`'s delivery worker, has been
verified live against a real running process and (for `#18`) real external infrastructure.

## What doesn't exist yet

- **Node**: replay (`#19` — must implement async replay expansion per `docs/adr/0005`),
  visibility/read API (`#20`), test suite + deployment docs (`#21` — owes the closing tests for
  5 "Design fixed" review findings: F-1, F-2, F-5, F-8, F-10, now implementable since `#18`
  exists; also owes `docs/adr/0004`'s tarpit-tenant fairness `make load` scenario, and real
  multi-worker same-endpoint concurrency races, both explicitly deferred from `#17`/`#18`).
- **Go**: the entire stack (`#22-27`), mirroring Node ticket-for-ticket — including every
  review-driven fix, not the pre-review design. The Go schema must match Node's *current*
  `001_init.sql`, not an earlier version.
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
