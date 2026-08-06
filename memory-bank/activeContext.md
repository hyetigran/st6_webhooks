# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

Implementation phase, Node track. Five of six Node tickets are built and merged: schema/
scaffolding (`#16`), publish/async expansion (`#17`), the delivery worker (`#18`), replay
(`#19`), and the visibility/read API (`#20`). Node now has a working, live-verified publish →
expand → deliver → replay → read pipeline end to end, including real HTTP delivery to a public
receiver. Only `#21` (test suite & deployment) remains on the Node track. Go (`#22-27`) and the
frontend (`#28-30`) haven't started.

## What just happened (most recent session)

- **`#20` — [Node] Visibility & read API built and merged** (MR !5, branch
  `20-node-visibility-read-api`, off an up-to-date `main` after `#19`'s MR !3 was merged on
  request). TDD throughout, seams confirmed with the user first (this ticket had more open
  response-shape judgment calls than usual, since `DECISIONS.md`'s route table pins paths/
  purposes but not exact JSON shapes). Four routes: `GET /events` (search by type/endpoint_id/
  from/to/id, R-24, `endpoint_id` filters via `EXISTS` through deliveries), `GET /events/:id`
  (fan-out summary), `GET /deliveries/:id` (state/attempt_count/next_attempt_at/
  `blocked_on_delivery_id` per R-23, `last_response`, attempts capped at 6 per the contract),
  `GET /endpoints/:id/deliveries` (queue view — **ascending** by `seq`, head first, its own
  `after`-param seq cursor rather than the other list routes' `before`/`created_at`+id
  convention, since a queue view's natural order is the opposite of a list view's newest-first).
  - **`blocked_on_delivery_id`** (R-12/R-23, `CONTEXT.md`'s "Blocked" definition: not the
    endpoint's current head, head hasn't resolved) is computed read-time from each endpoint's
    oldest unresolved (`pending`/`in_flight`) delivery by `seq` — shared between the two routes
    that need it via new `node/src/lib/deliveryQueries.ts` (`HEAD_DELIVERY_SELECT` SQL fragment,
    `computeBlockedOnDeliveryId`, `serializeDeliverySummary`).
  - R-25's health fields (`queue_depth`/`oldest_pending_at`/`recent_success_rate` on
    `GET /endpoints`) were already built in `#16` — confirmed correct, nothing to add.
  - **Live-verified against real infrastructure**: queried all four new routes against real
    historical data (a real `httpbin.org`-delivered event and its original + replayed
    deliveries) through the actual running API — real captured response bodies, correct
    filtering, correct 404s.
  - `/code-review` (Standards + Spec axes) found and fixed two real issues: (1) standards —
    `deliveries.ts` and `endpoints.ts` were duplicating the same delivery-summary
    serialization inline instead of sharing it, extracted `serializeDeliverySummary` into
    `deliveryQueries.ts`; (2) spec — the attempts array on `GET /deliveries/:id` had no
    explicit `LIMIT`, silently relying on `config.backoff.maxAttempts`'s default (also 6) rather
    than enforcing the contract's independent "capped at 6" requirement — fixed with an explicit
    `LIMIT 6` (newest-first, reversed for a chronological response) and a regression test
    proving the cap keeps the *most recent* 6 attempts, not the first 6.
- **`#19` — [Node] Replay, previously built, merged into `main` on request** (MR !3).

## Next steps

- `#21` — [Node] Test suite & deployment — the last Node ticket. Owns `make chaos`/
  `make properties`/`make load`, the actual closing tests for review findings F-1/F-2/F-5/F-8/
  F-10, `docs/adr/0004`'s stated tarpit-tenant fairness bound, and real multi-worker
  same-endpoint concurrency races (all explicitly deferred from `#17`/`#18`/`#19`). Also owes
  `#20`'s `replays.status` polling exposure per `docs/adr/0005`'s "Consequences" note, if that
  hasn't been picked up elsewhere by then.
- `#22` — [Go] Schema, scaffolding & endpoint management API (mirrors `#16`, unblocked, fully
  independent of the Node track). **The Go schema must include `deliveries.seq` from the start**
  (not just mirror `001_init.sql` — also apply `002_deliveries_seq.sql`'s change), since both
  stacks must implement `docs/adr/0007` identically. Also: `GET /endpoints/:id/deliveries`'s
  `after`/seq-cursor convention (deliberately different from every other list route) must match
  exactly for the shared frontend (ADR-008) to work against either backend.
- `#28` — Frontend: API client & endpoint management UI (buildable now against the fixed REST
  contract — all four `#20` read routes plus everything from `#16`-`#19` are available).

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
