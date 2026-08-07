# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

**The Node track is done.** The Go track has started: ticket `#22` ([Go] Schema, scaffolding &
endpoint management API) is built, merged, and verified — the schema and the endpoint-management
REST surface exist at `go/`. Tickets `#23`-`#27` (publish/expansion, delivery worker, replay,
visibility, test suite & deployment) are not yet built. The frontend (`#28-30`) hasn't started.

## What just happened (most recent session)

- **`#22` — [Go] Schema, scaffolding & endpoint management API built and merged** (MR !7, branch
  `22-go-schema-scaffolding-endpoint-api`, off an up-to-date `main` after `#21`'s MR !6). First
  Go-track ticket — installed Go 1.26 via Homebrew (wasn't present on the dev machine) and
  confirmed two tooling forks with the user before scaffolding: stdlib `net/http` (Go 1.22+
  method+`{wildcard}` `ServeMux` routing) over `chi`, and `pgx`/`pgxpool` over `lib/pq`+
  `database/sql`. Both chosen to keep the stack dependency-light, matching Node's "no ORM, no
  framework beyond what's load-bearing" precedent.
  - **Schema** (`go/internal/db/migrations/001_init.sql`): matches Node's *current* schema
    exactly — all five tables, same columns/constraints/indexes — but `deliveries.seq` is a plain
    `BIGSERIAL` column from the start (`docs/adr/0007`) in one migration file, not a second
    `ALTER TABLE` the way Node's `002_deliveries_seq.sql` needed, since Go never had a pre-seq
    schema to migrate away from. Migrations are embedded into the binary via `go:embed` (a
    Go-specific simplification over Node's Dockerfile, which has to separately copy the
    migrations directory into the image).
  - **Endpoint management API**: all six routes (register/list/detail/pause/resume/
    rotate-secret), Bearer auth resolving to `tenant_id` (`internal/auth`), R-2 SSRF-defense URL
    validation (`internal/validation`, same denylist logic as Node's `validation/url.ts`), R-25
    health fields (queue_depth/oldest_pending_at/recent_success_rate, same subquery shape as
    Node's `HEALTH_SELECT`), R-14/`REVIEW.md` F-12 resume disclosure
    (`skipped_failed_delivery_ids` computed identically regardless of prior status). Credentials:
    SHA-256 for API keys, AES-256-GCM for signing secrets (`internal/crypto`) — same layout as
    Node's (`iv || authTag || ciphertext`, base64), verified with a round-trip unit test.
  - Verified: `go vet` + `gofmt` clean, 19 tests (`go test ./...`) against a real Postgres
    instance (`webhooks_go_test`, port 5533) via `net/http/httptest` — no mocks, same seam
    pattern Node used (supertest → httptest). Live curl smoke test against a running server and
    real dev Postgres, every route exercised including R-2 rejection.
  - **`/code-review` (Standards + Spec axes) found and fixed two real Node-contract
    divergences**, both regression-tested:
    1. Unauthenticated requests to an *unmatched* path were returning 404 instead of 401 — Go's
       original route table only wrapped the six known routes in `requireTenant`, leaving the
       catch-all unauthenticated. Node's `app.use(requireTenant, endpointsRouter, ...)` runs
       before its own 404 fallback, so *every* non-`/healthz` request needs a valid key first,
       matched-route-or-not. Fixed by nesting the whole route table (including its own 404
       handler) inside `requireTenant`.
    2. Registration validation was stricter than Node's zod schemas for whitespace-only input —
       Go had added `strings.TrimSpace(...) == ""` checks that reject inputs Node's
       `z.string().min(1)` (a raw length check) accepts. Fixed to match Node's raw-length
       semantics exactly: a whitespace-only `url` now correctly falls through to
       `url_not_allowed` (not a stricter `invalid_request`), and a whitespace-only `event_types`
       entry is now accepted, same as Node.
    - Standards axis also flagged (fixed): duplicated `pgx.ErrNoRows→404`/`else→500`
      error-handling repeated across every handler (extracted a shared `fail()` helper), a
      repeated 6-column row-scan (extracted `scanEndpointRow`, mirroring the existing
      `scanEndpointHealthRow`), missing doc comments on several exported identifiers, and a
      `fmt.Println`/`log.Printf` logging-idiom inconsistency in `db/migrate.go`.
  - **Port choices**: Go's isolated Postgres runs on host port **5533** (Node's is 5532), API on
    **8090** — 5432/5433/5532 and 8080-8083 were all already taken on the dev machine, confirmed
    via `lsof` before picking.

## Next steps

- `#23` — [Go] Publish & async expansion (mirrors Node's `#17`, unblocked). `POST /events` with
  `Idempotency-Key` idempotency (ON CONFLICT + fallback SELECT, same bug class Node's ticket
  caught — a naive `ON CONFLICT DO NOTHING` returns zero rows on conflict, so a follow-up SELECT
  is required to return the *original* event's id/status) and the worker's expansion cycle using
  `docs/adr/0001`'s per-tenant `pg_try_advisory_xact_lock` serialization. Will need a Go worker
  entrypoint (`go/cmd/worker/`, doesn't exist yet) and pgx's equivalent of Node's `worker.ts`
  poll loop.
- `#24` — [Go] Delivery worker: ordering, crash recovery, fairness, signing, backoff & halt
  (mirrors Node's `#18`). **Must not repeat the R-11 claim-query bug** found in Node's `#21` via
  chaos testing — see `progress.md`'s gotchas: the true head (lowest `seq`) must always be
  fetched unconditionally, with `next_attempt_at` eligibility checked in application code, never
  filtered in the claim query's `WHERE` clause.
- `#25`-`#27` — [Go] Replay, Visibility & read API, Test suite & deployment (mirror Node's
  `#19`-`#21`). `#27` must build the identical PRD §8 acceptance suite shape Node's `#21`
  established (`make test`/`properties`/`chaos`/`load`/`verify`, real spawned processes/signals
  for chaos, real spawned api/worker for load) — Go's process-signal handling will differ from
  Node's `tsx`-wrapper gotcha (see `progress.md`), but needs its own equivalent verification.
- `#28` — Frontend: API client & endpoint management UI (buildable now against either backend's
  fixed REST contract — Go's `#22` gives it a second real backend to target, not just Node's).
- **Primary-build designation** (ticket `#14`'s decision) still needs Go's full `make load`
  result once `#27` exists, to compare against Node's baseline in `evidence/load/`.

## Open questions / risks being watched

- **C-2 (time spent) still needs the user's input** — flagged in `REVIEW.md`, not answered.
- Timebox: building two full implementations was a deliberate scope choice, with an explicit
  accepted fallback now written into `DECISIONS.md`'s Submission section (ship Node alone if Go
  isn't at parity by the timebox's midpoint). Go now has its first ticket done — worth revisiting
  this checkpoint once a few more Go tickets land.
- The Go unit/integration test suite (`go test`, real Postgres) deliberately does **not** yet
  cover real multi-worker concurrency races — same deferral Node made until its own `#21`'s
  chaos suite; Go's `#27` will need the equivalent.
- **`docs/adr/` numbering (`0001`-`0007`) and the wayfinder map's own decision numbering
  (`ADR-001` through `ADR-008`, referenced in the map's Decisions-so-far section and some older
  code comments) are two different numbering spaces that happen to look identical.** Already
  caused one real ambiguity (`#19`'s `ADR-007`/`ADR-0007` collision, fixed). When citing an ADR
  in new code or docs, prefer linking the actual `docs/adr/NNNN-slug.md` file over a bare
  "ADR-NNN" string wherever there's any chance of confusion with the map's numbering.
