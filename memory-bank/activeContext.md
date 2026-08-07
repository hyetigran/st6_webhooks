# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

**The Node track is done.** The Go track has five tickets done: `#22` (schema/scaffolding/
endpoint API), `#23` (publish & async expansion), `#24` (delivery worker), `#25` (replay), `#26`
(visibility & read API) — `go/` now has the full REST surface plus the async pipeline, all
verified live. Only `#27` (test suite & deployment) remains before Go matches Node's scope. The
frontend (`#28-30`) hasn't started.

## What just happened (most recent session)

- **`#26` — [Go] Visibility & read API built and merged** (MR !11, branch
  `26-go-visibility-read-api`, off an up-to-date `main` after `#25`'s MR !10). Full mechanism
  detail lives in `progress.md`'s "What works," not repeated here.
  - Built the four read routes (`GET /events`, `GET /events/:id`, `GET /deliveries/:id`,
    `GET /endpoints/:id/deliveries`) plus a new `internal/api/delivery_queries.go`, ported from
    Node's already-reviewed `events.ts`/`deliveries.ts`/`deliveryQueries.ts`.
  - Verified: 23 new tests green on first run against real Postgres, plus a live e2e read-API
    pass against postman-echo.com — see `progress.md`'s "What works" for what that proved.
  - `/code-review`'s Spec axis found **zero issues** — reported as a faithful port, including the
    three highest-risk areas (queue-view pagination direction, attempts-cap-keeps-most-recent,
    EXISTS-not-JOIN for the endpoint filter) all independently verified correct. Standards axis
    fixed dead code, missing doc comments, a cursor validity guard, and reconciled a
    query-building style inconsistency within the package (see `progress.md`'s gotchas).

## Next steps

- `#27` — [Go] Test suite & deployment (mirrors Node's `#21`, the last Go ticket) — must build
  the identical PRD §8 acceptance suite shape Node established (`make test`/`properties`/`chaos`/
  `load`/`verify`, real spawned processes/signals for chaos, real spawned api/worker for load).
  Go's process-signal handling will differ from Node's `tsx`-wrapper gotcha (see `progress.md`),
  but needs its own equivalent verification. With `#24`'s goroutine pool real, Go's `make load`
  results should be genuinely comparable to Node's on multi-core behavior, not just a coincidence
  of matching architecture.
- `#28` — Frontend: API client & endpoint management UI (buildable now against either backend's
  fixed REST contract — both are essentially feature-complete now).
- **Primary-build designation** (ticket `#14`'s decision) still needs Go's full `make load`
  result once `#27` exists, to compare against Node's baseline in `evidence/load/`.

## Open questions / risks being watched

- **C-2 (time spent) still needs the user's input** — flagged in `REVIEW.md`, not answered.
- Timebox: building two full implementations was a deliberate scope choice, with an explicit
  accepted fallback written into `DECISIONS.md`'s Submission section (ship Node alone if Go isn't
  at parity by the timebox's midpoint). Go now has five tickets done, one (`#27`) from parity with
  Node's full scope — worth revisiting this checkpoint once `#27` lands.
- The Go unit/integration test suite (`go test`, real Postgres) deliberately does **not** yet
  cover real multi-worker concurrency races — same deferral Node made until its own `#21`'s chaos
  suite; Go's `#27` will need the equivalent (and now has a real multi-goroutine worker to
  actually chaos-test, unlike a hypothetical single-threaded one).
- **Go's test suite must run with `go test -p 1 ./...` (or `make test`), never bare `go test
  ./...`** — every package's tests share the one `webhooks_go_test` database via `TRUNCATE`, so
  default cross-package parallelism flakes.
- **`docs/adr/` numbering (`0001`-`0007`) and the wayfinder map's own decision numbering
  (`ADR-001` through `ADR-008`, referenced in the map's Decisions-so-far section and some older
  code comments) are two different numbering spaces that happen to look identical.** Already
  caused one real ambiguity (`#19`'s `ADR-007`/`ADR-0007` collision, fixed). When citing an ADR in
  new code or docs, prefer linking the actual `docs/adr/NNNN-slug.md` file over a bare "ADR-NNN"
  string wherever there's any chance of confusion with the map's numbering.
