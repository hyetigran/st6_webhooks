# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

**The Node track is done.** The Go track has four tickets done: `#22` (schema/scaffolding/
endpoint API), `#23` (publish & async expansion), `#24` (delivery worker), `#25` (replay) — `go/`
now has a working publish → expand → deliver → replay pipeline with a real concurrent worker
pool. Tickets `#26`-`#27` (visibility & read API, test suite & deployment) are not yet built. The
frontend (`#28-30`) hasn't started.

## What just happened (most recent session)

- **`#25` — [Go] Replay built and merged** (MR !10, branch `25-go-replay`, off an up-to-date
  `main` after `#24`'s MR !9). Same async two-phase shape as publish (`docs/adr/0005`) — full
  mechanism detail lives in `progress.md`'s "What works," not repeated here.
  - Built `POST /endpoints/{id}/replays` and `internal/worker.RunReplayExpansionCycle`, ported
    from Node's already-reviewed `replays.ts`/`replayExpansion.ts`.
  - Verified: 11 new tests green on first run against real Postgres. Live e2e: published and
    delivered a real event to postman-echo.com (httpbin.org was down at the time), replayed it,
    watched the worker expand and successfully redeliver via a fresh delivery row reusing the
    original `event_id` — confirming R-19/R-20 for real. Bonus: an earlier delivery against the
    down httpbin.org correctly halted at `failed` after exhausting retries.
  - `/code-review` found and fixed a real REST-surface divergence: Go's `time.Parse` accepted
    non-UTC timezone offsets (e.g. `+01:00`) that Node's `z.string().datetime()` rejects by
    default — fixed with an explicit `Z`-suffix check, regression-tested. Also fixed a
    misleadingly-named variable and a missing rollback-safety comment.

## Next steps

- `#26`-`#27` — [Go] Visibility & read API, Test suite & deployment (mirror Node's `#20`-`#21`).
  `#27` must build the identical PRD §8 acceptance suite shape Node's `#21` established (`make
  test`/`properties`/`chaos`/`load`/`verify`, real spawned processes/signals for chaos, real
  spawned api/worker for load) — Go's process-signal handling will differ from Node's `tsx`-
  wrapper gotcha (see `progress.md`), but needs its own equivalent verification. With `#24`'s
  goroutine pool real, Go's `make load` results should be genuinely comparable to Node's on
  multi-core behavior, not just a coincidence of matching architecture.
- `#28` — Frontend: API client & endpoint management UI (buildable now against either backend's
  fixed REST contract).
- **Primary-build designation** (ticket `#14`'s decision) still needs Go's full `make load`
  result once `#27` exists, to compare against Node's baseline in `evidence/load/`.

## Open questions / risks being watched

- **C-2 (time spent) still needs the user's input** — flagged in `REVIEW.md`, not answered.
- Timebox: building two full implementations was a deliberate scope choice, with an explicit
  accepted fallback written into `DECISIONS.md`'s Submission section (ship Node alone if Go isn't
  at parity by the timebox's midpoint). Go now has four tickets done — worth revisiting this
  checkpoint once `#26`-`#27` land.
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
