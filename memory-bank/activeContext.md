# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

**Both backend tracks are done; the frontend track (`#28-30`) is underway.** `#28`/`#29` are
done. Only `#30` remains before the whole map is done, plus the primary-build designation
synthesis step.

## What just happened (most recent session)

- **`#29` — Frontend: event/delivery detail & endpoint queue views built and merged** (MR !14 +
  a follow-up code-review-fix commit, branch `29-frontend-event-delivery-queue-views`, off an
  up-to-date `main` after `#28`'s merge). Full mechanism detail lives in `progress.md`'s "What
  works," not repeated here.
  - Built PRD §7 surfaces 2-4 (event detail, delivery detail — "the primary screen," endpoint
    queue with a real halted-head highlight) plus the Events search list. Deliberately did not
    fabricate the mockup's "what we sent" header/signature preview since the real API doesn't
    return that data — grounded every field in the actual response shape instead.
  - Extracted `useEndpointActions`/`EndpointActionModals` (shared across the endpoints list and
    the new endpoint-detail view) proactively, before this ticket could triple that logic.
  - `/code-review`: both axes found real issues — Standards found duplicated
    `deliveryTone`/`Field`/`Breadcrumb`/next-attempt-display logic across the three new pages and
    a genuine `formatRelativeTime` bug (future timestamps read "0s ago" instead of "in Xs");
    Spec found the head-highlight wasn't actually a highlight and the interactive resume flow had
    leaked into `#29` when it's `#30`'s explicit scope. All fixed in a follow-up commit,
    re-verified live against both backends before merging.

## Next steps

- `#30` — Frontend: replay UI & polish, blocked by `#28`+`#29` (now both satisfied) — builds the
  replay-trigger UI, integrates the resume-from-halted flow into `#29`'s endpoint queue view
  (deliberately deferred there), and adds an Overview dashboard home page plus a
  loading/error/empty-state polish pass across every view.
- **Primary-build designation** (ticket `#14`'s decision) can now actually happen — both stacks
  have real `make load` evidence (`evidence/load/` for Node, `evidence/go/load/` for Go). Nobody
  has run the comparison yet; this is a synthesis step, not a new ticket, likely worth doing once
  the frontend track is also done (or sooner, if the user wants the call made now).

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
