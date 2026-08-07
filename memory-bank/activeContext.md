# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

**Both backend tracks are done; the frontend track (`#28-30`) is underway.** `#28` (API client +
endpoint management UI) is done. `#29`/`#30` remain before the whole map is done, plus the
primary-build designation synthesis step.

## What just happened (most recent session)

- **`#28` — Frontend: API client & endpoint management UI built and merged** (MR !13 + a
  follow-up code-review-fix commit, branch `28-frontend-api-client-endpoint-ui`, off an
  up-to-date `main` after `#27`'s merge) — the frontend track's foundation ticket. Full mechanism
  detail lives in `progress.md`'s "What works," not repeated here.
  - Scaffolded `frontend/` (Vite + React + TS + TanStack Query + React Router). TDD'd the typed
    API client against issue #13's contract. Built PRD §7 surface 1, a runtime backend switcher,
    and the landing page (agreed bonus scope) — design closely follows the pasted mockup, tokens
    extracted via computed-style inspection of its live-rendered pages, not eyeballed.
  - Found and fixed two real bugs while live-verifying against both real backends in an actual
    browser (not curl): neither backend had CORS configured at all (the first genuine
    cross-origin client either ever saw), and a TanStack Query retry/refetchInterval interaction
    could hide a persistent error in permanent pending state — see `progress.md`'s gotchas.
  - `/code-review`: Spec axis found zero issues. Standards axis found a data clump, duplicated
    query-building logic, and silent pause/resume/rotate failures — all fixed in a follow-up
    commit, re-verified live against both backends before merging.

## Next steps

- `#29` — Frontend: event/delivery detail & endpoint queue views (PRD §7 surfaces 2-4), blocked
  by `#28` (now satisfied) — builds directly on `#28`'s API client, design system, and
  backend-switcher/auth plumbing.
- `#30` — Frontend: replay UI & polish, blocked by `#28`+`#29`.
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
