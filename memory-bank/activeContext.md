# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

**The Node track is done.** The Go track has two tickets done: `#22` (schema, scaffolding,
endpoint management API) and `#23` (publish & async expansion) are built, merged, and verified —
`go/` now has a working publish → expand pipeline plus a running worker process. Tickets `#24`-
`#27` (delivery worker, replay, visibility, test suite & deployment) are not yet built. The
frontend (`#28-30`) hasn't started.

## What just happened (most recent session)

- **`#23` — [Go] Publish & async expansion built and merged** (MR !8, branch
  `23-go-publish-async-expansion`, off an up-to-date `main` after `#22`'s MR !7). Same TDD seams
  as `#22` — real Postgres, `httptest`/direct-pool calls, no forks worth re-confirming.
  - **`POST /events`** (`go/internal/api/events.go`): `Idempotency-Key` header required,
    unique-constraint + `ON CONFLICT DO NOTHING` + fallback SELECT for the idempotent-repeat case
    (same bug class as Node's `REVIEW.md` F-11 — a suppressed conflict returns zero rows, so a
    follow-up SELECT is required to return the *original* event's id/status, not a fresh one).
  - **`internal/worker.RunExpansionCycle`** (new package): per-tenant
    `pg_try_advisory_xact_lock(hashtext(tenant_id)::bigint)` serialization per `docs/adr/0001` —
    the ticket's literal text says "claim via `FOR UPDATE SKIP LOCKED`", which is stale (predates
    the ADR-0001 correction); implemented per the ADR instead, exactly the same correction Node's
    `#17` needed. Claims a tenant's oldest `pending_expansion` event by `seq`, fans out to every
    subscribed endpoint regardless of status (active/paused/halted all get queued), flips to
    `expanded` — one transaction, using a real `pgx.Tx` rather than raw SQL `BEGIN`/`COMMIT`.
  - **New `go/cmd/worker` entrypoint**: the shared worker pool process, mirrors
    `node/src/worker.ts`'s poll loop shape (try every cycle, sleep only when none found work).
    Currently just expansion — delivery (`#24`) and replay expansion (`#25`) join the same loop
    as their tickets land.
  - **Two real bugs found and fixed** — full mechanism/fix detail lives in `progress.md`'s
    gotchas section, not repeated here: a cross-package `go test` parallelism flake (`internal/
    api`/`internal/worker` share one Postgres test database), and `/code-review` independently
    catching `payload: null` bypassing validation via a `json.Unmarshal`-into-map quirk.
  - Standards axis also flagged (fixed): test-fixture duplication between `internal/api` and
    `internal/worker`'s test packages (`setupPool`/`createTenant`/`createEndpoint`) — extracted
    into a new `internal/testsupport` package, imported only from `*_test.go` files so
    `testify`/`testing` never leak into a built binary.
  - Verified: `go vet`/`gofmt` clean, full suite green under `make test`, live end-to-end smoke
    test with real spawned API + worker processes and real Postgres — publish, repeat-publish
    idempotency observed mid-expansion (status flips from `pending_expansion` to `expanded`
    between the two calls), and the worker actually creating the delivery row.

## Next steps

- `#24` — [Go] Delivery worker: ordering, crash recovery, fairness, signing, backoff & halt
  (mirrors Node's `#18`, unblocked). **Must not repeat the R-11 claim-query bug** found in Node's
  `#21` via chaos testing — see `progress.md`'s gotchas: the true head (lowest `seq`) must always
  be fetched unconditionally, with `next_attempt_at` eligibility checked in application code,
  never filtered in the claim query's `WHERE` clause. This ticket extends `go/cmd/worker`'s poll
  loop (already has expansion) with the delivery cycle, same one-shared-process pattern as Node.
- `#25`-`#27` — [Go] Replay, Visibility & read API, Test suite & deployment (mirror Node's
  `#19`-`#21`). `#27` must build the identical PRD §8 acceptance suite shape Node's `#21`
  established (`make test`/`properties`/`chaos`/`load`/`verify`, real spawned processes/signals
  for chaos, real spawned api/worker for load) — Go's process-signal handling will differ from
  Node's `tsx`-wrapper gotcha (see `progress.md`), but needs its own equivalent verification.
- `#28` — Frontend: API client & endpoint management UI (buildable now against either backend's
  fixed REST contract).
- **Primary-build designation** (ticket `#14`'s decision) still needs Go's full `make load`
  result once `#27` exists, to compare against Node's baseline in `evidence/load/`.

## Open questions / risks being watched

- **C-2 (time spent) still needs the user's input** — flagged in `REVIEW.md`, not answered.
- Timebox: building two full implementations was a deliberate scope choice, with an explicit
  accepted fallback now written into `DECISIONS.md`'s Submission section (ship Node alone if Go
  isn't at parity by the timebox's midpoint). Go now has two tickets done — worth revisiting this
  checkpoint once a few more Go tickets land.
- The Go unit/integration test suite (`go test`, real Postgres) deliberately does **not** yet
  cover real multi-worker concurrency races — same deferral Node made until its own `#21`'s
  chaos suite; Go's `#27` will need the equivalent.
- **Go's test suite must run with `go test -p 1 ./...` (or `make test`), never bare `go test
  ./...`** — every package's tests share the one `webhooks_go_test` database via `TRUNCATE`, so
  default cross-package parallelism flakes. This will keep applying as more `go/internal/*`
  packages grow their own tests.
- **`docs/adr/` numbering (`0001`-`0007`) and the wayfinder map's own decision numbering
  (`ADR-001` through `ADR-008`, referenced in the map's Decisions-so-far section and some older
  code comments) are two different numbering spaces that happen to look identical.** Already
  caused one real ambiguity (`#19`'s `ADR-007`/`ADR-0007` collision, fixed). When citing an ADR
  in new code or docs, prefer linking the actual `docs/adr/NNNN-slug.md` file over a bare
  "ADR-NNN" string wherever there's any chance of confusion with the map's numbering.
