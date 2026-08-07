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
- **Pipeline diagram's dot/highlight desync found and fixed, live-verified in Chrome** (the Chrome extension reconnected this
  session, closing out the "not live-verified" gap from the previous two passes) — the user reported "the pipeline UI shifted
  once more" after the diagram landed. Root cause: the moving "packet" dot animated its position via a CSS transition (an
  810ms glide) while the active box's border highlight switched instantly on the same `stage` change — two independent
  animation schedules driven by the same state, so a screenshot mid-transition could show the dot sitting in one box while a
  completely different box had the highlighted border (confirmed: caught the border on "Delivery loop" while the dot was
  still gliding near "Events"). Fixed by deleting the floating cross-diagram dot entirely and rendering a small pulsing
  indicator (new `gr-pulse` keyframes in `index.css`) as a child of the active box itself, driven by the exact same
  `stage === i` conditional that colors the border — both now come from one check in one render, so they can't disagree.
  Verified live: toggled Implementation/Traffic band/Scenario controls and Play/Pause repeatedly, confirmed the pulse and
  border always land on the same box every time. Committed straight to `main` (`50ca930`) — no ticket/MR.
- **Pipeline diagram's flowing dot restored, this time properly synced** — the previous fix traded away the actual point of
  the diagram (a packet flowing through the pipeline, per the mockup) to kill the desync; the user asked for the flow back.
  Replaced discrete tick-driven `stage` state with a continuous `progress` value (advanced every 60ms) — the dot's position,
  which box's border is highlighted, and which receiver a lap targets are now all pure derivations of `progress` computed in
  the same render, so there's no second animation timeline (no CSS transition) for them to drift apart on again. Added static
  SVG connector lines between boxes (`viewBox="0 0 100 100"` + `preserveAspectRatio="none"` maps 1:1 onto the same left/top
  percentages the boxes use) so the motion reads as flowing along a wire, closer to the mockup's own canvas-drawn routing. The
  dot holds still while "at" a receiver rather than flying back to the Publisher — that return trip isn't a real hand-off.
  Live-verified in Chrome: watched it glide along each leg across multiple ticks, confirmed no disagreement with the
  highlighted box, confirmed Pause freezes it mid-glide. Committed straight to `main` (`fec90db`) — no ticket/MR.
- **Pipeline diagram's wiring redrawn to match the mockup pixel-for-pixel** (user asked to "review design mockups and
  replicate the pipeline section pixel perfect") — rendered `design mockups/Gauntlet Relay Landing.dc.html` live in Chrome
  (via a local `python3 -m http.server`, since `file://` is blocked for the extension and the mockup needs its own
  `support.js`/`_ds_bundle.js` runtime to render the `{{ }}` bindings) and found three real fidelity gaps against the
  built `PipelineDiagram` (`frontend/src/pages/Landing.tsx`): (1) `SegGroup` rendered as separate gapped pill buttons
  instead of the mockup's `.seg`/`.seg-opt` — one joined, bordered control with divider lines between options; (2) the
  connector lines between boxes were straight diagonals, not the mockup's right-angle "circuit-board" routing with
  dashed vs. solid edges and small arrowheads; (3) the receiver status tag used the shared `Badge` component's flat
  neutral-gray styling for both states, where the mockup's `tag-accent` (delivering) is filled `--color-accent-100`/
  `--color-accent-800` and its `tag-outline` (idle) is a bordered, transparent-fill pill — `Badge`'s own comment claims
  it matches the mockup, but doesn't once actually screenshotted live. Fixed all three, scoped entirely to
  `Landing.tsx`: rebuilt `SegGroup` as the joined control; replaced the diagonal-line SVG with a `WIRES` backdrop of
  orthogonal polylines (`elbowH`/`elbowV` helpers) plus one added dashed Expansion→deliveries edge (a true relationship
  the mockup leaves implicit rather than wiring explicitly — added so the traveling dot always rides a visible line for
  every hop rather than cutting a diagonal through open space for that one leg); added a local `PipelineTag` component
  instead of touching the shared `Badge` (which is used across every dashboard view with its own deliberate flat-badge
  rationale — changing its default would have ripple effects well outside this section's scope). The traveling dot's
  hard-won continuity invariant (see the two fixes above) is preserved: each hop's point-array still starts and ends at
  a box **center**, so consecutive hops share an exact endpoint — only the path *between* those centers now bends
  through the same edge points the backdrop wire uses, via a new arc-length-parametrized `pointAlongPath`, rather than
  lerping straight center-to-center. Live-verified in Chrome side-by-side against the rendered mockup at 1440px:
  segmented controls, dashed/solid wire styling, arrowhead direction, and both tag states (idle outline / delivering
  filled) all confirmed matching; watched the dot ride multiple hops with no jump or desync. `npx tsc -b --noEmit` and
  `npx vitest run` both clean. Committed to `main` (`a974c59` + a follow-up memory-bank commit `29299ee`).

- **Two real bugs the user caught by eye after the wiring pass above, both fixed the same session**: (1) traffic-band labels
  were "Quiet"/"Moderate"/"Flood" (a paraphrase, kept from before the pixel-fidelity pass) instead of the mockup's literal
  "1K"/"10K"/"1M+" — renamed the `Band` type and every `Record<Band, …>` keyed by it (`BAND_MULTIPLIER`, `PUBLISH_P99_MS`),
  same underlying evidence-file mapping (10/1000/10000 concurrent-publish levels), just matching the mockup's own text now.
  (2) "the scenario suite is overflowing on second row" — real, reproduced via direct `getBoundingClientRect` measurement
  (visual screenshots at the widths tried didn't show it clearly; `resize_window` wasn't reliably changing `innerWidth` in
  this session's Chrome instance, so the repro had to go through injected DOM measurement instead of eyeballing). Root
  cause: the controls row's `alignItems: "flex-end"` (copied faithfully from the mockup) is only harmless in the mockup's
  own 1440px-wide layout where the scenario segmented control never wraps; this app's narrower 1100px content column plus
  longer scenario labels (`"Partition drains"`, `"Tarpit fairness"` vs. the mockup's shorter placeholders) make it wrap to
  two lines routinely, and `flex-end` then pulls the single-row Implementation/Traffic-band columns down to align with the
  *wrapped* column's full height — landing their buttons even with the scenario row's second line instead of its first.
  Fixed by anchoring the row to `flex-start` and bottom-aligning only the Pause/Play button via its own `alignSelf:
  "flex-end"` (preserves the original flush-bottom look for that button when nothing wraps, without dragging the other
  columns down when something does). Confirmed via the same DOM-measurement technique: Implementation/Traffic-band/
  scenario-row-1 all now share the same `top`, at both a wrapped width and the original full width.

- **Single traveling dot replaced with a many-packet stream** (user: "only one blue dot travels through the pipeline. design
  shows dozens to hundreds passing") — correct, and something the very first pixel-fidelity pass had already noticed and
  chosen not to build, on the assumption it was out of scope; the user's follow-up made it in-scope. Pulled the single
  dot's position math (stageIndex/lap/receiverIndex/t/point, all derived from one continuous `progress`) out into a
  standalone `packetState(p)` (`frontend/src/pages/Landing.tsx`), then render `packetCount = bandMultiplier * 12` packets —
  each just `packetState(progress + i * phaseStep)`, phase-shifted copies of the same clock spread evenly across the full
  cyclic path length — instead of one. Reuses `BAND_MULTIPLIER` (already real: same multiplier the stat counters use), so
  1K/10K/1M+ render 12/36/108 concurrent dots — sparse-but-present up to a genuinely dense continuous stream, matching what
  the mockup's own diagram shows at higher bands. Box/receiver highlighting changed from "does the one dot's stageIndex
  match this box" to "does ANY packet currently occupy this box" (`activeNodeStages`/`activeReceivers` sets built once per
  render) — with dozens of packets in flight, several boxes legitimately light up at once now, which is more honest than
  pretending only one box is ever active. The continuity guarantee from the wiring pass still holds per-packet (each one
  still starts/ends every hop at a box center via `HOP_PATHS`) — no new per-packet state, no CSS transitions, still a pure
  function of one `progress` value. Live-verified in Chrome across all three bands: 1K reads as "a few," 10K as "dozens,"
  1M+ as a dense stream filling every wire simultaneously; no console errors at 108 concurrent packets. `npx tsc -b
  --noEmit`, `npx oxlint`, `npx vitest run` all clean.

- **1M+ band's packets moved at the same pace as 1K's** (user: "the dots move extremely slow") — real gap: `progress`
  advanced at one fixed rate (`FRAME_MS/TICK_MS` per interval tick) regardless of band, so the only thing that scaled with
  traffic was packet *count*, not speed — 108 packets crawling at the same one-stage-per-900ms pace as 12 packets read as
  sluggish for the "extreme throughput" band. Fixed by multiplying the per-tick `progress` increment by the same
  `bandMultiplier` already driving packet count (`frontend/src/pages/Landing.tsx`'s `progress` interval effect, now
  depending on `bandMultiplier` too so switching bands actually retunes the running interval) — not a second invented
  number: 1M+ moving 9x faster than 1K is the identical real ratio already behind 9x the packet count, and it happens to
  agree with the real evidence too (`PUBLISH_P99_MS` genuinely drops at higher concurrency in this app's own load tests).
  Live-verified in Chrome: switched to 1M+, watched the published/delivered counters and the active receiver visibly
  advance within about a second (45→117 published, receiver highlight moved from Billing Service to Inventory Sync) —
  clearly faster than before, motion still smooth, no console errors. `npx tsc -b --noEmit`, `npx oxlint`, `npx vitest
  run` all clean.

- **Console dashboard (`Overview.tsx`) content/presentation pass** (standalone, user-requested review + "implement all" —
  not a ticket, done on branch `worktree-dashboard-polish` in parallel with the pipeline-diagram work above) — the
  Overview page was visibly the weakest console screen next to the detail views (`EndpointDetail.tsx`/
  `DeliveryDetail.tsx`), which already had dense, well-labeled real-data copy. Fixed: `StatCard` gained an optional `danger`
  tone (used on "Endpoints needing you" when count > 0) and every stat card now carries a context caption (endpoint counts,
  reporting coverage, halted/paused breakdown) instead of a bare number with no way to tell "zero" from "no data yet."
  "Needs attention" now sorts halted before paused (severity, not API order); the Recent events status column uses the
  existing `Badge` component instead of plain text (matching `EventDetail.tsx`'s own tone convention); the backend name is
  bolded in the subhead so a stat is harder to misread against the wrong backend. New `design/Grid.css`
  (`.app-grid-auto` via `auto-fit`/`minmax`, `.app-grid-split` with one stacking breakpoint at 860px) replaces every fixed
  `gridTemplateColumns` inline style across the console (`Overview.tsx`, `EndpointDetail.tsx`, `DeliveryDetail.tsx`,
  `EventDetail.tsx`) — grepping the whole `frontend/src` tree beforehand found **zero** `@media` queries anywhere, so none
  of these screens degraded gracefully below their designed width. Deliberately did **not** add a "recent failures" panel
  (the improvement that would have most directly served the support-engineer user) — no tenant-wide deliveries endpoint
  exists (only per-endpoint/per-event), and `Overview.tsx`'s own existing comment already documents that an N+1 fan-out
  fetch for a summary page was ruled out when `#30` first built this page; the severity-sorted Needs attention list is the
  failure-visibility mechanism instead. Live-verified in Chrome: real Node-backend data end to end, and the responsive
  breakpoint specifically (the extension's screenshot viewport doesn't track OS window resize, so confirmed via
  `document.styleSheets` that the compiled `@media (max-width: 860px)` rule loaded, then force-applied it via an injected
  `<style>` to screenshot the actual stacked layout). Also hit and worked around, not fixed: **port 3000 on this dev
  machine is bound by an unrelated project** (`ghost-chess`, an Expo app at `/Users/tig/Desktop/tigran/ghost-chess`) — the
  real webhooks Node API on this machine actually runs on **3001**, with the main frontend dev server started as
  `VITE_NODE_API_URL=http://localhost:3001 npm run dev` to route around it. `frontend/src/lib/backend.tsx`'s hardcoded
  `http://localhost:3000` default is otherwise correct as *shipped* config — this is purely a local-machine port
  collision, not a bug, but worth knowing before assuming "everything's already running" from `lsof` port-name output
  alone next time. Committed to branch `worktree-dashboard-polish` (`d53e24e`), merged into `main` via `c3e0d59`.

- **User locked the landing page** (`frontend/src/pages/Landing.tsx`, including `PipelineDiagram`) after the pixel-fidelity
  pass above: don't modify it again without explicit permission each time, even as a side effect of other work. Saved as a
  standing feedback memory (not just here) so it survives across sessions.

- **Heavy-traffic seed scripts built** (`node/scripts/seedHeavyTraffic.ts`, `go/cmd/seedheavytraffic/main.go`, both
  uncommitted) — user wanted the console dashboard to look interesting under real volume rather than near-empty demo data.
  Both go through the real pipeline end to end (real publish-shaped inserts → real expansion → real HTTP delivery through
  the real worker cycles), not fabricated delivery outcomes — the one sanctioned exception is the same local-receiver
  SSRF-bypass seam `chaos/worker-entrypoint.ts`/`cmd/chaosworker` already established (never weakening the real check).
  Ten endpoint profiles (healthy/flaky/deterministic-retry/always-fails/paused/slow), one large shared event stream plus
  several small dedicated ones so fan-out — not raw event count — does the heavy lifting cheaply (expansion is
  serialized-per-tenant, so total event count is the real throughput ceiling, not delivery-row count). Result on both
  backends: 110,041 (Node) / 110,040 (Go) delivery rows, all genuinely resolved (halted-by-design and paused-by-design
  endpoints aside) after a follow-up fix — see progress.md's gotchas for the process-management saga this took to get a
  clean run, and for the completion-detection design lesson (wait for expansion, then a bounded delivery window — not
  full drain, which is an unbounded tail single-flight-per-endpoint makes very slow).

## Next steps

- Nothing outstanding on the landing page — all pipeline-diagram follow-up passes (evidence panel, pipeline diagram build,
  dot/highlight fix, dot restore, wiring pixel-fidelity pass, band-label/overflow fixes, multi-packet stream, packet-speed
  scaling) are live-verified and committed/merged to `main`. **Do not touch this page without the user's explicit
  permission first** (see above).
- Console dashboard polish pass above is committed and merged to `main` — nothing further queued there either.
- **Heavy-traffic seed scripts are uncommitted** — `node/scripts/seedHeavyTraffic.ts` and `go/cmd/seedheavytraffic/` sit
  in the working tree; ask the user whether they want them committed (as dev tooling, not part of `make verify`/test/
  chaos/load) or left as a one-off.

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
