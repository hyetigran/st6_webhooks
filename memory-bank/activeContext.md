# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

Implementation phase, Node track. Three of six Node tickets are built and merged: schema/
scaffolding (`#16`), publish/async expansion (`#17`), and the delivery worker (`#18`). Node now
has a working, live-verified publish → expand → deliver pipeline end to end, including real
HTTP delivery to a public receiver. Go (`#22-27`) and the frontend (`#28-30`) haven't started.

## What just happened (most recent session)

- **`#18` — [Node] Delivery worker built and merged** (MR !2, branch `18-node-delivery-worker`,
  off an up-to-date `main` after `#17`'s MR !1 was found already merged). TDD throughout, seams
  confirmed with the user first. New modules: `node/src/worker/delivery.ts` (`claimDelivery`,
  `completeDelivery`, `computeBackoffDelayMs`, `runDeliveryCycle`), `node/src/worker/
  httpClient.ts` (`resolveAndPin`, `sendOutboundRequest` — the SSRF-safe pinned HTTP client),
  `node/src/lib/signing.ts` (`signPayload`, multi-secret HMAC). `worker.ts` now runs expansion
  and delivery in one shared poll loop, not two processes.
  - This is what **closes the five "Design fixed" `REVIEW.md` findings** (F-1, F-2, F-5, F-8,
    F-10) that were waiting on the delivery worker to exist — their closing behavior (advisory
    locks, lease fencing, tenant fairness, resolve-validate-pin) is now real, running code with
    passing tests, not just schema/ADRs.
  - **Live-verified against real infrastructure, not mocks**: registered an endpoint pointing at
    `https://httpbin.org/post` through the real API (real registration-time SSRF check passed),
    published an event, and watched the real running worker process expand → claim → sign →
    resolve-validate-pin (real DNS) → deliver → mark succeeded. httpbin's echo response
    confirmed every `Webhook-*` header and the exact signature.
  - `/code-review` (Standards + Spec axes) surfaced two real, acted-on findings: missing test
    coverage for the ADR-0003 multi-secret-signing rotation path (added), and `errorClass`
    typed as a bare `string` instead of a closed union (fixed in both `httpClient.ts` and
    `delivery.ts`). Also caught and fixed a genuine flaky-test bug unrelated to the review: the
    `createPendingDelivery` fixture compared a Node-clock timestamp against Postgres's own
    `now()`, which real Node/Docker-Postgres clock skew made intermittently false — confined to
    test fixtures, never a production bug (see `progress.md`'s gotchas).
  - Config cleanup: `outboundHttp.maxRedirects` removed (dead — superseded by
    `docs/adr/0006`'s "never follow redirects"), `maxConnectionsPerHost` added (R-16's per-host
    limit, via `http.Agent`/`https.Agent`'s `maxSockets`).
- **`#17` — [Node] Publish & async expansion, previously built, confirmed merged** (MR !1 — was
  already merged, likely by the user directly, before this session checked).
- **A stale README disclosure line was removed a second time**, by the user directly (commit
  `0af1d5b`, outside any agent session) — confirmed during `#18`'s planning that this is
  intentional this time, not a repeat of the earlier accident noted below `#17`'s entry. Don't
  re-restore it.

## Next steps (unblocked, parallel-eligible)

- `#19` — [Node] Replay (unblocked by `#18`; must implement `docs/adr/0005`'s async replay
  expansion, mirroring publish's pattern — not a naive synchronous re-delivery loop).
- `#20` — [Node] Visibility & read API (unblocked by `#18`).
- `#21` — [Node] Test suite & deployment — owns `make chaos`/`make properties`/`make load`, the
  actual closing tests for review findings F-1/F-2/F-5/F-8/F-10 and `docs/adr/0004`'s stated
  tarpit-tenant fairness bound (both now implementable since `#18` exists).
- `#22` — [Go] Schema, scaffolding & endpoint management API (mirrors `#16`, unblocked, fully
  independent of the Node track).
- `#28` — Frontend: API client & endpoint management UI (buildable now against the fixed REST
  contract).
- MR !2 (`#18`) is open, not yet merged into `main` — confirm with the user before merging, same
  as was done for !1.

## Open questions / risks being watched

- **C-2 (time spent) still needs the user's input** — flagged in `REVIEW.md`, not answered.
- Timebox: building two full implementations was a deliberate scope choice, with an explicit
  accepted fallback now written into `DECISIONS.md`'s Submission section (ship Node alone if Go
  isn't at parity by the timebox's midpoint).
- The unit/integration test suite (vitest, real Postgres) deliberately does **not** cover real
  multi-worker concurrency races (two processes actually contending over one endpoint's busy
  flag) — that's explicitly deferred to `#21`'s chaos suite, same precedent set when `#17` was
  built. `#18`'s `claimDelivery` does have one concurrency test (two *different* endpoints
  claimed in parallel via `Promise.all` against the real pool), but not a same-endpoint race.
