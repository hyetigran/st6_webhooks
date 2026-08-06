# Active Context

The most volatile file in the memory bank — update this at the end of any session that changes
what's being worked on. If this file and reality disagree, trust the wayfinder map (GitLab
issue #1) and `git log`, then fix this file.

## Current phase

Planning and an adversarial review pass are both complete. Architecture is now hardened, not
just designed — a full review (`REVIEW.md`, 17 findings) has been run against it and every
finding closed. This is the **implementation phase** — 15 build tickets across Node, Go, and
the shared frontend, one of which (`#16`) is built.

## What just happened (most recent session)

- **`CONTEXT.md` was built** via a `/grill-with-docs` (grilling + domain-modeling) session — 13
  terms defining the project's ubiquitous language (Event, Delivery, Attempt, Endpoint, Tenant,
  Receiver, Claim, Lease, Expansion, Replay, Queue, Halted, Blocked), each with an `_Avoid_`
  list redirecting loose synonyms.
- **An adversarial review (`REVIEW.md`) was run** against `ARCHITECTURE.md`/`DECISIONS.md`/
  `CONTEXT.md`/`PRD.md`/`COMPARISON.md` by an external pass, surfacing 17 findings (4 Critical,
  6 High, 3 Medium, 4 Low) plus a 6-item CASE_STUDY compliance checklist. **All 17 findings and
  4 of 6 checklist items are now resolved** — see `REVIEW.md`'s burn-down table for exact status
  per finding (most are "Fixed"; five are "Design fixed," meaning the schema/docs/ADR are
  committed but the closing test needs the delivery worker, ticket `#18`, which doesn't exist
  yet).
- **Six new ADRs** were created in `docs/adr/` (`0001`–`0006`), each correcting a real design
  gap the review found: per-tenant expansion serialization (ordering bug), lease fencing
  (stalled-worker state corruption), sender-side multi-sign secret rotation (rotation was
  silently halting endpoints), a stated tenant-fairness bound, async replay expansion (mirrors
  publish, fixes a crash-unsafe idempotency gap), and SSRF resolve-validate-pin + no-redirects.
- **Schema changed**: `events.seq`, `endpoints.lease_id`/`secondary_secret`/
  `secondary_secret_expires_at`, `replays.status`, `tenants.api_key_hash` (renamed from
  `api_key`). `node/src/db/migrations/001_init.sql` is current.
- **Already-shipped code (`#16`) was fixed and re-verified live**, twice: secret rotation
  storage, `resume`'s `skipped_failed_delivery_ids` disclosure, and credential handling
  (API keys now SHA-256-hashed, signing secrets AES-256-GCM-encrypted at rest — both tested
  end-to-end against a real Postgres instance, including a full encrypt/decrypt round-trip
  check). `npm run typecheck` passes clean throughout.
- **`DECISIONS.md` was compressed** from a peak of ~2644 words (absorbing all 17 findings) to
  ~1472, leaning on the new `docs/adr/` files for detail instead of re-explaining inline.
- **C-1** (the `reqs not read` omission note) restored to the top of `README.md` — it had gone
  missing in an earlier edit. **Update**: the user removed it again themselves in a later
  session (commit `0af1d5b`, "remove wording from readme") and confirmed during ticket #17's
  code review that this second removal is intentional, not a repeat of the earlier accident —
  don't re-restore it. **C-3**: user confirmed willing to share session transcripts; noted in
  `README.md`. **C-2** (time spent) is still open — genuinely needs the user's answer, can't be
  filled in by an agent.
- Every Mermaid diagram in `ARCHITECTURE.md` (8 total) was validated against the real parser,
  not just eyeballed — this caught real syntax bugs (HTML entities and mid-message semicolons
  break Mermaid's sequence-diagram grammar) more than once this session.

## Next steps (unblocked, parallel-eligible)

- `#17` — [Node] Publish & async expansion (unblocked by `#16`; must implement the per-tenant
  advisory-lock serialization from `docs/adr/0001`, not the naive parallel version).
- `#22` — [Go] Schema, scaffolding & endpoint management API (mirrors `#16`, including all the
  review-driven fixes — the Go schema must match Node's post-review schema exactly, not the
  pre-review one).
- `#28` — Frontend: API client & endpoint management UI (buildable now against the fixed REST
  contract).
- Not urgent, but real: commit the current working tree (see `progress.md` / `git status`).

## Open questions / risks being watched

- **C-2 (time spent) still needs the user's input** — flagged in `REVIEW.md`, not answered.
- Timebox: building two full implementations was a deliberate scope choice, with an explicit
  accepted fallback now written into `DECISIONS.md`'s Submission section (ship Node alone if Go
  isn't at parity by the timebox's midpoint).
- `DECISIONS.md` is still somewhat over the "about two pages" budget (~1472 words vs. the
  original ~1050) — expected, since it now records 17 additional fixes on top of the original
  14 decisions, not the same content restated. Judged acceptable, not further compressed.
- The five "Design fixed" (not yet "Fixed") review findings (F-1, F-2, F-5, F-8, F-10) all share
  the same blocker: their closing tests need the delivery worker (`#18`). Whoever builds `#18`
  should read those findings' resolution notes in `REVIEW.md` first — they specify exact
  mechanisms (advisory locks, lease fencing, resolve-validate-pin) that must be implemented
  correctly, not just "however seems reasonable."
