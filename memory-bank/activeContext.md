# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

**The Node track is done.** The Go track has three tickets done: `#22` (schema/scaffolding/
endpoint API), `#23` (publish & async expansion), `#24` (delivery worker) — `go/` now has a
working publish → expand → deliver pipeline with a real concurrent worker pool. Tickets `#25`-
`#27` (replay, visibility, test suite & deployment) are not yet built. The frontend (`#28-30`)
hasn't started.

## What just happened (most recent session)

- **`#24` — [Go] Delivery worker built and merged** (MR !9, branch `24-go-delivery-worker`, off
  an up-to-date `main` after `#23`'s MR !8). The largest Go ticket so far — full mechanism detail
  (claim ordering, lease fencing, R-11's fix, signing, timeout classification) lives in
  `progress.md`'s "What works" and gotchas, not repeated here.
  - Built `ClaimDelivery`/`CompleteDelivery`/`RunDeliveryCycle`, `internal/signing`, and
    `internal/worker/httpclient.go` (`ResolveAndPin`/`SendOutboundRequest`) — all ported from
    Node's already-reviewed `delivery.ts`/`httpClient.ts`/`signing.ts`, including the R-11 fix
    carried over exactly.
  - **Implemented a real goroutine pool** in `go/cmd/worker` (`WORKER_POOL_SIZE`, default
    `runtime.NumCPU()`) — `/code-review`'s Spec axis caught that this was genuinely asked for in
    the ticket text ("Goroutine pool for concurrency... per ADR-001") and confirmed it's a
    Go-specific requirement (Node's identically-scoped `#18` never asked for it — Node achieves
    concurrency differently, via its single-threaded event loop). This matters beyond just this
    ticket: ADR-001's whole reason for building Go was to measure *real* multi-core behavior, and
    a single-threaded Go worker would never exercise that for the eventual `#14` primary-build
    load comparison.
  - Verified: 39 new tests all green on first run against real Postgres + real `httptest`
    receivers, live e2e delivery to httpbin.org, and a live concurrency check (5 endpoints × 5
    events, multiple simultaneous `in_flight` deliveries observed mid-run — real parallelism, not
    just a fast sequential loop).
  - `/code-review` also found and fixed: missing doc comments, a redundant `tx.Rollback()`, split
    an overgrown `tryClaimEndpoint` into three helpers, and a genuine test-timing flake (a
    timestamp comparison with no tolerance that could spuriously fail on a near-zero backoff
    jitter draw — see `progress.md`'s gotchas).

## Next steps

- `#25`-`#27` — [Go] Replay, Visibility & read API, Test suite & deployment (mirror Node's
  `#19`-`#21`). `#27` must build the identical PRD §8 acceptance suite shape Node's `#21`
  established (`make test`/`properties`/`chaos`/`load`/`verify`, real spawned processes/signals
  for chaos, real spawned api/worker for load) — Go's process-signal handling will differ from
  Node's `tsx`-wrapper gotcha (see `progress.md`), but needs its own equivalent verification. With
  `#24`'s goroutine pool now real, Go's `make load` results should actually be comparable to
  Node's on multi-core behavior, not just a coincidence of matching architecture.
- `#28` — Frontend: API client & endpoint management UI (buildable now against either backend's
  fixed REST contract).
- **Primary-build designation** (ticket `#14`'s decision) still needs Go's full `make load`
  result once `#27` exists, to compare against Node's baseline in `evidence/load/`.

## Open questions / risks being watched

- **C-2 (time spent) still needs the user's input** — flagged in `REVIEW.md`, not answered.
- Timebox: building two full implementations was a deliberate scope choice, with an explicit
  accepted fallback written into `DECISIONS.md`'s Submission section (ship Node alone if Go isn't
  at parity by the timebox's midpoint). Go now has three tickets done — worth revisiting this
  checkpoint once `#25`-`#27` land.
- The Go unit/integration test suite (`go test`, real Postgres) deliberately does **not** yet
  cover real multi-worker concurrency races — same deferral Node made until its own `#21`'s chaos
  suite; Go's `#27` will need the equivalent (and now has a real multi-goroutine worker to
  actually chaos-test, unlike a hypothetical single-threaded one).
- **Go's test suite must run with `go test -p 1 ./...` (or `make test`), never bare `go test
  ./...`** — every package's tests share the one `webhooks_go_test` database via `TRUNCATE`, so
  default cross-package parallelism flakes. This will keep applying as more `go/internal/*`
  packages grow their own tests.
- **`docs/adr/` numbering (`0001`-`0007`) and the wayfinder map's own decision numbering
  (`ADR-001` through `ADR-008`, referenced in the map's Decisions-so-far section and some older
  code comments) are two different numbering spaces that happen to look identical.** Already
  caused one real ambiguity (`#19`'s `ADR-007`/`ADR-0007` collision, fixed). When citing an ADR in
  new code or docs, prefer linking the actual `docs/adr/NNNN-slug.md` file over a bare "ADR-NNN"
  string wherever there's any chance of confusion with the map's numbering.
