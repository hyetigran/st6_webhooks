# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

Implementation phase, Node track. Four of six Node tickets are built and merged: schema/
scaffolding (`#16`), publish/async expansion (`#17`), the delivery worker (`#18`), and replay
(`#19`). Node now has a working, live-verified publish → expand → deliver → replay pipeline end
to end, including real HTTP delivery to a public receiver. Go (`#22-27`) and the frontend
(`#28-30`) haven't started.

## What just happened (most recent session)

- **`#19` — [Node] Replay built and merged** (MR !3, branch `19-node-replay`, off an
  up-to-date `main` after `#18`'s MR !2 was merged on request). TDD throughout, seams confirmed
  with the user first. New: `node/src/routes/replays.ts` (`POST /endpoints/:id/replays`,
  mirrors `events.ts`'s idempotency pattern exactly), `node/src/worker/replayExpansion.ts`
  (`runReplayExpansionCycle`, plain `FOR UPDATE SKIP LOCKED` claim — deliberately *not* the
  per-tenant advisory lock event expansion uses, since replay has no cross-replay ordering
  guarantee to protect, only its own-batch order). `worker.ts` now runs three cycles
  (expansion, replay expansion, delivery) in one shared poll loop.
  - **Found and fixed a real, previously-undiscovered ordering bug while implementing**, not
    part of `REVIEW.md`: Postgres's `now()` is transaction-stable (every statement in one
    transaction sees the identical value), so replay's multi-row same-endpoint
    `INSERT...SELECT` would give every new row an identical `created_at`, breaking "original
    chronological order" via arbitrary tie-breaking. Expansion never hit this because one
    event's expansion inserts at most one delivery per endpoint. **Confirmed the fix with the
    user before implementing** (real trade-off, schema change): added `deliveries.seq
    BIGSERIAL` (migration `002_deliveries_seq.sql`, new **`docs/adr/0007`**), mirroring
    `events.seq`'s established fix for the same class of problem. `delivery.ts`'s claim query
    and claimable-candidate tiebreak now sort by `seq`, not `created_at`. Migration `001_init.sql`
    is no longer edited retroactively for schema changes — from this point on, schema changes are
    additive migration files, since real databases now have applied migration history.
  - **Live-verified against real infrastructure**: replayed a previously-`succeeded` delivery
    (from `#18`'s smoke test, to `https://httpbin.org/post`) through the real running worker —
    fresh `delivery_id`, same `event_id` reused (R-20), claimed and delivered for real,
    `succeeded`.
  - `/code-review` (Standards + Spec axes): spec axis fully clean. Standards axis found and
    fixed a real pre-existing ambiguity this diff exposed — a stale comment in `001_init.sql`
    said `ADR-007` (the *wayfinder map's own* decision-numbering, issue `#8`, predating the
    `docs/adr/` folder) for tenant fairness, which now collides in a grep with the new,
    unrelated `docs/adr/0007`. Fixed to point at `docs/adr/0004-tenant-fairness-bound.md`
    explicitly. Also fixed a minor error-message wording inconsistency between `events.ts` and
    `replays.ts`.
  - `test/fixtures.ts`'s `createPendingDelivery` was renamed to `createDelivery` and
    generalized (`state`/`createdAt` options) to support replay's terminal-vs-pending window
    tests — all three existing call-site files updated, no behavior change to existing tests.
- **`#18` — [Node] Delivery worker, previously built, merged into `main` on request** (MR !2).

## Next steps (unblocked, parallel-eligible)

- `#20` — [Node] Visibility & read API (unblocked by `#18`; also the natural place to expose
  `replays.status` for polling, per `docs/adr/0005`'s "Consequences" note — not yet in the REST
  contract).
- `#21` — [Node] Test suite & deployment — owns `make chaos`/`make properties`/`make load`, the
  actual closing tests for review findings F-1/F-2/F-5/F-8/F-10, `docs/adr/0004`'s stated
  tarpit-tenant fairness bound, and real multi-worker same-endpoint concurrency races (all
  explicitly deferred from `#17`/`#18`/`#19`).
- `#22` — [Go] Schema, scaffolding & endpoint management API (mirrors `#16`, unblocked, fully
  independent of the Node track). **The Go schema must include `deliveries.seq` from the start**
  (not just mirror `001_init.sql` — also apply `002_deliveries_seq.sql`'s change), since both
  stacks must implement `docs/adr/0007` identically.
- `#28` — Frontend: API client & endpoint management UI (buildable now against the fixed REST
  contract).

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
