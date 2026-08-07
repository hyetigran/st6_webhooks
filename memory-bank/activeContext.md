# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

**The entire wayfinder map is implementation-complete.** Both backend tracks (Node `#16-21`, Go
`#22-27`) and the frontend track (`#28-30`) are done — every child ticket on the map is closed.
The only work left on the whole project is the primary-build designation synthesis (see "Next
steps") — not a ticket, a decision-application step using data that now exists.

## What just happened (most recent session)

- **`#30` — Frontend: replay UI & polish built and merged** (MR !15 + a follow-up
  code-review-fix commit, branch `30-frontend-replay-polish`, off an up-to-date `main` after
  `#29`'s merge) — the last ticket on the entire map. Full mechanism detail lives in
  `progress.md`'s "What works," not repeated here.
  - Built the replay-trigger UI and integrated resume-from-halted into `#29`'s endpoint queue
    view (deliberately deferred there). Added the Overview dashboard home page (bonus scope) and
    a 404 route.
  - `/code-review`: Spec found a real bug — the new Overview page had zero error handling and
    silently showed a false "everything's healthy" message on an actual fetch failure (verified
    live: a bad API key now correctly shows the real error) — and that the ticket's own "polish
    pass across all views" had left 3 of 5 pre-existing views untouched (fixed: extracted shared
    `LoadingState`/`ErrorState` components, applied everywhere). Standards found a silent
    pagination-truncation gap, a `busy`-state composition gap, a missed query invalidation, and a
    `{id,url}` data clump. All fixed in a follow-up commit, re-verified live before merging.
- **Standalone `/code-review` against the whole frontend track** (`89f6e93...HEAD`, all of
  `#28`+`#29`+`#30` combined, at the user's request after `#30` merged) — caught issues invisible
  to any single ticket's own review: a duplicated input-style object (extracted
  `design/TextInput.tsx`), two competing row-click patterns (reconciled), a fabricated "24h"
  label on a field that isn't time-windowed, the endpoint-history filter the API client had
  supported since `#28` but no view ever used (wired up), and `DeliveryDetail.tsx`'s breadcrumb
  going blank on error since it needed fetched data to build its links. Fixing that last one
  surfaced a real, separate, more serious bug while live-verifying: a persistent 5xx (not just a
  bad API key) left the page in permanent limbo. Root-caused via direct instrumentation — see
  `progress.md`'s gotchas for the corrected, complete fix (`retry: false` app-wide; the earlier
  `#28` fix was a partial patch that only masked the 4xx case). Committed straight to `main` (no
  ticket/MR — this wasn't ticket-scoped work).

- **Landing page brought closer to the mockup** (standalone, user-requested after noticing the drift — not a ticket) — the mockup
  (`Webhook API server landing and dashboard/Gauntlet Relay Landing.dc.html`) has a "Latest run" evidence panel, a closing CTA
  with a delivery-timeline illustration, and a footer that the original build (ticket `#28`) never carried over; only the design
  tokens (fonts/colors/spacing) had been faithfully extracted, not the full page content. Added all three. The evidence panel
  deliberately does **not** reuse the mockup's own numbers ("0 of 50,000", "1,200 crashes", "under 180ms") — those were
  placeholder marketing figures with no real evidence behind them. Replaced with real numbers read from
  `evidence/{load,chaos}/*.json` and `evidence/go/{load,chaos}/*.json`: 3 of 3 properties, 5 of 5 chaos scenarios, and the real
  measured noisy-neighbor p99s (226ms Node / 127ms Go) against the load test's own bound (p99 < 5s — the spec sets no fixed
  number, see `node/load/noisy-neighbor.ts`). Deliberately skipped the mockup's fully-interactive animated pipeline diagram
  (`#instrument` — region/backend/scenario pickers, live throughput/p99/worker stats) since replicating it needs either a real
  live-simulation backend or fabricated numbers; the existing static "The pipeline" card grid already covers the same content
  honestly. Committed straight to `main` (`77a7edf`) — no ticket/MR.
- **Landing page's centerpiece pipeline diagram, added after the user pushed back that it was still missing** — the mockup's
  `#instrument` section (an interactive blueprint diagram: implementation/traffic-band/scenario pickers, play/pause, an
  animated packet moving through the pipeline, a live stats panel) had been skipped in the prior pass on the assumption it
  couldn't be done honestly. That assumption was wrong: the mockup carries its own disclaimer for this exact panel ("Time is
  compressed and the dots are representative — the ordering and gating you see are the real ones"), so a client-side
  simulation with the same disclaimer is consistent with the mockup's own framing, not a new compromise. Built as
  `PipelineDiagram` in `frontend/src/pages/Landing.tsx` — same box layout/labels as the mockup, segmented controls, and a
  900ms-tick animated dot stepping through the real stage sequence. Real inputs: the runtime toggle (matches the app's actual
  Node/Go backend switcher, ADR-008), scenario names (the actual named tests under `node/{load,chaos}` and `go/{load,chaos}`),
  worker count (3, matches `WORKER_COUNT` in every load/chaos harness), and the "Publish p99" stat (real numbers from
  `evidence/load/publish-latency-flat.json` / `evidence/go/load/publish-latency-flat.json`, keyed by traffic band → event
  level). Illustrative: Published/Queue depth/In flight/Delivered are a compressed-time simulation, stated in the panel's own
  disclaimer line. Committed straight to `main` (`707f879`) — no ticket/MR.
- **Neither landing-page pass has been live-verified in a real browser** — the Chrome extension wasn't connected in either
  session; typecheck/lint/test/build are all clean and the new code only composes already-verified design primitives (`Card`,
  `Badge`, `Button`), but an actual visual/interaction check (does the diagram's dot actually animate smoothly, do the
  segmented controls read correctly at the mockup's `min-width: 1000px` diagram width) is still owed.

## Next steps

- **Verify the landing page visually** in a real browser, especially the new `PipelineDiagram` — skipped twice in a row only
  because the Chrome extension wasn't connected, not because it was judged low-risk enough to skip on purpose.

- **Primary-build designation** (ticket `#14`'s already-decided criteria) is the one remaining
  piece of work on the whole project — both stacks now have real `make load` evidence
  (`evidence/load/` for Node, `evidence/go/load/` for Go) to apply it to. Nobody has run the
  actual comparison yet. This is a synthesis/decision step, not a wayfinder ticket (the map has
  no open children left) — worth doing whenever the user wants the primary-build call made.
- Beyond that, see "Open questions / risks being watched" below — nothing new this session.

## Open questions / risks being watched

- **C-2 (time spent) still needs the user's input** — flagged in `REVIEW.md`, not answered.
- Timebox: building two full implementations was a deliberate scope choice, with an explicit
  accepted fallback written into `DECISIONS.md`'s Submission section (ship Node alone if Go isn't
  at parity by the timebox's midpoint). Go reached full parity with Node's scope as of `#27` — the
  fallback was never needed.
- **`README.md` at the repo root is still the interim stub** — stays that way until the primary
  build is chosen (see "Next steps" above) and its own README supersedes the root file.
- **Go's test suite must run with `go test -p 1 ./...` (or `make test`), never bare `go test
  ./...`** — every package's tests share the one `webhooks_go_test` database via `TRUNCATE`, so
  default cross-package parallelism flakes.
- **`docs/adr/` numbering (`0001`-`0007`) and the wayfinder map's own decision numbering
  (`ADR-001` through `ADR-008`, referenced in the map's Decisions-so-far section and some older
  code comments) are two different numbering spaces that happen to look identical.** Already
  caused one real ambiguity (`#19`'s `ADR-007`/`ADR-0007` collision, fixed). When citing an ADR in
  new code or docs, prefer linking the actual `docs/adr/NNNN-slug.md` file over a bare "ADR-NNN"
  string wherever there's any chance of confusion with the map's numbering.
