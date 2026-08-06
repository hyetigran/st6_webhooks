# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

**The entire Node track is done.** All six Node tickets (`#16`-`#21`) are built, merged, and
verified. Node has a complete, live-verified publish → expand → deliver → replay → read
pipeline, a full PRD §8 acceptance suite (`make test`/`properties`/`chaos`/`load`/`verify`), a
real clone-to-run `node/README.md`, and a validated Docker Compose deployment. All 17 original
`REVIEW.md` findings are genuinely `Fixed`. Nothing left to do on Node unless the Go/primary-
build comparison surfaces something. Go (`#22-27`) and the frontend (`#28-30`) haven't started —
these are next.

## What just happened (most recent session)

- **`#21` — [Node] Test suite & deployment built and merged** (MR !6, branch
  `21-node-test-suite-deployment`, off an up-to-date `main` after `#20`'s MR !5 was merged on
  request). This was the largest ticket in the map — four distinct testing paradigms. Confirmed
  tooling choices with the user first: hand-written seeded-PRNG invariants over `fast-check`
  (only ~3 invariants, not worth a new dependency), hand-rolled Node/TS load scripts over
  `autocannon` (the fairness scenarios need custom two-tenant-simultaneously shapes a generic
  load tool isn't suited to).
  - **`make test`**: F-3 secret rotation overlap (`rotation.overlap.test.ts` — signs with both
    secrets throughout the window, never halts, fails after expiry), backoff-schedule
    reconstruction from real `attempts` timestamps (`backoff.schedule.test.ts` — halts exactly
    on the `maxAttempts`-th failure), paused-endpoint exclusion (R-4), a slow-loris receiver
    (trickles the body forever — distinct from a merely-delayed response), and a
    conforming-receiver fixture (`conformingReceiver.ts`/`.test.ts`, F-13/R-20 — proves a replay
    of a previously-*failed* event is genuinely reprocessed, not silently no-op'd by a receiver
    that dedupes correctly on success).
  - **`make properties`** (`test/properties/`, seeded via `prng.ts`'s mulberry32 — every run
    logs its seed, reproducible via `PROPERTY_TEST_SEED`): seq order under racing concurrent
    expansion workers, repeated publish-key idempotency, replay crash-safety (retries before
    expansion still land exactly once).
  - **`make chaos`** (`node/chaos/`, real spawned processes, real `SIGKILL`/`SIGSTOP`/
    `SIGCONT`): kill-mid-delivery, F-2's stall-fencing (the actual `SIGSTOP` → reclaim → `SIGCONT`
    → dropped-write scenario, not simulated), partition-head-blocked-then-drains, crash-after-
    successful-send, and `expansion-crash-order` (a real process `SIGKILL`ed while holding a
    tenant's expansion advisory lock — proves `docs/adr/0001`'s "no lease needed" claim under an
    actual crash).
  - **`make load`** (`node/load/`, real spawned api/worker pool): publish/replay latency flat
    (10→10,000 subscribers, 100→10,000-delivery windows), noisy-neighbor volume fairness, F-5's
    tarpit-tenant bound — **measured ~1020-1150ms against a 1000ms outbound timeout**, landing
    almost exactly on `docs/adr/0004`'s "roughly one outbound-timeout cycle."
  - **A real, previously-undetected bug found via chaos testing, not inspection**: the delivery
    claim query picked the oldest *eligible* pending delivery rather than the true head, so a
    later delivery could jump the queue while the head was mid-backoff — a genuine R-11
    violation. Fixed in `node/src/worker/delivery.ts` (head fetched unconditionally, eligibility
    checked in application code), regression-covered in `delivery.claimDelivery.test.ts`.
  - **A real, non-obvious infrastructure bug**: `tsx`'s own CLI re-execs into a *second* inner
    node process to satisfy Node's loader-hook API — `spawn()` only gets a handle to the outer
    wrapper, and `SIGKILL` is uncatchable, so a killed wrapper can't relay it to its child,
    leaving the real worker process orphaned and un-killable. Fixed by invoking `node` directly
    with `tsx`'s loader flags, bypassing the wrapper — see `progress.md`'s gotchas.
  - Chaos/load scenarios can't deliver to local receivers through the *real* `src/worker.ts`
    (its SSRF check correctly rejects loopback) — solved with a dedicated,
    never-shipped `chaos/worker-entrypoint.ts` mirroring the real poll loop exactly except for
    an injected permissive resolver, via the same `DeliveryCycleDeps` seam the vitest suite uses.
  - **Full Docker Compose stack built and validated end-to-end** — real delivery to a real
    external receiver through the actual containers, not just local `tsx` dev mode.
  - `node/README.md` rewritten from an interim stub into the real clone-to-run guide — every
    command and curl example in it was actually run and checked against real output, including
    the HMAC verification snippet (computed and matched byte-for-byte against a live signature).
  - `/code-review` (Standards + Spec axes) found and fixed two real issues: (1) standards —
    `chaos/harness.ts` and `load/harness.ts` had near-identical database-bootstrap/polling/
    evidence-writing code with no shared module, extracted into `node/scripts/scenarioHarness.ts`;
    (2) spec — PRD §8's concurrent-expansion-ordering row names *both* `make chaos` and
    `make properties`, but only the properties half existed — added `expansion-crash-order.ts`.
  - Added `LEASE_MIN_DURATION_MS` (`node/src/config.ts`) — the lease-duration floor was
    hardcoded at 30s with no env override, unlike every other timing knob, which would have
    made every lease-expiry chaos scenario take 30s+ regardless of other config.

## Next steps

- `#22` — [Go] Schema, scaffolding & endpoint management API (mirrors `#16`, unblocked, fully
  independent of the Node track). **The Go schema must include `deliveries.seq` from the start**
  (not just mirror `001_init.sql` — also apply `002_deliveries_seq.sql`'s change), since both
  stacks must implement `docs/adr/0007` identically. Also: `GET /endpoints/:id/deliveries`'s
  `after`/seq-cursor convention (deliberately different from every other list route) must match
  exactly for the shared frontend (ADR-008) to work against either backend. And: the delivery
  claim query bug found in `#21` (jumping the queue past a backing-off head) is a design-level
  gotcha the Go implementation must not repeat — see `progress.md`'s gotchas.
- `#28` — Frontend: API client & endpoint management UI (buildable now against the fixed REST
  contract — every route across `#16`-`#21` is available and live-verified).
- **Primary-build designation** (ticket `#14`'s decision) can now actually be measured once Go
  exists — Node's `make load` results are captured in `evidence/load/` as a baseline to compare
  against.

## Open questions / risks being watched

- **C-2 (time spent) still needs the user's input** — flagged in `REVIEW.md`, not answered.
- Timebox: building two full implementations was a deliberate scope choice, with an explicit
  accepted fallback now written into `DECISIONS.md`'s Submission section (ship Node alone if Go
  isn't at parity by the timebox's midpoint).
- The unit/integration test suite (vitest, real Postgres) deliberately does **not** cover real
  multi-worker concurrency races (two processes actually contending over one endpoint's busy
  flag, or two workers racing to expand the same replay) — explicitly deferred to `#21`'s chaos
  suite, same precedent set when `#17` was built. `#18`'s `claimDelivery` does have one
  concurrency test (two *different* endpoints claimed in parallel via `Promise.all` against the
  real pool), but not a same-endpoint race.
- **`docs/adr/` numbering (`0001`-`0007`) and the wayfinder map's own decision numbering
  (`ADR-001` through `ADR-008`, referenced in the map's Decisions-so-far section and some older
  code comments) are two different numbering spaces that happen to look identical.** Already
  caused one real ambiguity (`#19`'s `ADR-007`/`ADR-0007` collision, fixed). When citing an ADR
  in new code or docs, prefer linking the actual `docs/adr/NNNN-slug.md` file over a bare
  "ADR-NNN" string wherever there's any chance of confusion with the map's numbering.
