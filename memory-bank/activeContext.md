# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

**Both backend tracks are done.** Node (`#16-21`) and Go (`#22-27`) are both feature-complete and
pass the identical PRD §8 acceptance bar. Only the frontend (`#28-30`) and the primary-build
designation synthesis step remain before the whole map is done.

## What just happened (most recent session)

- **`#27` — [Go] Test suite & deployment built and merged** (MR !12 + a follow-up code-review-fix
  commit, branch `27-go-test-suite-deployment`, off an up-to-date `main` after `#26`'s MR !11) —
  the last Go-track ticket. Full mechanism detail lives in `progress.md`'s "What works," not
  repeated here.
  - Built `make properties`/`chaos`/`load`/`verify` for the Go stack, mirroring Node's `#21`
    shape exactly (5 chaos scenarios, 4 load scenarios, 3 property tests). Finalized
    `docker-compose.yml` (added the `worker` service) and rewrote `go/README.md`.
  - Found and fixed two real bugs while live-verifying `docker compose up` and a fresh clone
    (a migration race, a missing test-db auto-create) — see `progress.md`'s gotchas.
  - `/code-review`: Spec axis found zero issues. Standards axis found real scenario-code
    duplication and a genuine `sync.Once` bug in test setup — both fixed in a follow-up commit,
    re-verified with a full clean `make verify` run before merging.

## Next steps

- `#28` — Frontend: API client & endpoint management UI (buildable now against either backend's
  fixed REST contract — both backends are fully done).
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
