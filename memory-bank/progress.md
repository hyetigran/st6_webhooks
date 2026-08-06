# Progress

What works, what doesn't exist yet, and known issues. For live ticket-level status, the
wayfinder map (GitLab issue #1) is authoritative — this file is a snapshot for fast orientation,
not a replacement for it.

## What works

- **Node — endpoint management API** (`node/`): schema migrated (post-review — see below),
  Bearer auth (API key hashed, SHA-256), R-2 URL validation (IPv4/IPv6, literal + DNS-resolved
  private/loopback/link-local/CGNAT rejection), all six endpoint routes (register/list/detail/
  pause/resume/rotate-secret). Signing secrets encrypted at rest (AES-256-GCM). `resume`
  discloses `skipped_failed_delivery_ids` alongside `pending_delivery_count`, computed
  identically regardless of prior status. Verified live against a real Postgres instance
  multiple times, including after the credential-handling and resume fixes — see `#16`'s
  resolution comment and `REVIEW.md`'s F-3/F-12/F-16 resolution notes for test transcripts.
- **Full documentation set, adversarially reviewed**: `ARCHITECTURE.md`, `DECISIONS.md`,
  `CONTEXT.md`, `COMPARISON.md`, `PRD.md` all went through a 17-finding review (`REVIEW.md`) and
  came out corrected — this isn't just "written," it's been checked for internal consistency,
  arithmetic correctness, and real design bugs. 6 ADRs in `docs/adr/` record the corrections.
- **All 8 Mermaid diagrams in `ARCHITECTURE.md`** validated against the real parser (not just
  visual inspection) — publish/expand/deliver, crash recovery (dead worker + stalled worker,
  two separate diagrams), replay, both state diagrams, the system overview, and the ER diagram.

## What's built but not yet exercised end-to-end

Nothing currently — everything built so far has been verified live, more than once for the
pieces the review touched (rotation, resume, credentials).

## What doesn't exist yet

- **Node**: publish/expansion (`#17` — must implement `docs/adr/0001`'s per-tenant
  advisory-lock serialization, not naive parallel expansion), delivery worker (`#18` — must
  implement lease fencing per `docs/adr/0002`, resolve-validate-pin + no-redirects per
  `docs/adr/0006`, and multi-secret signing per `docs/adr/0003`), replay (`#19` — must implement
  async replay expansion per `docs/adr/0005`), visibility/read API (`#20`), test suite +
  deployment docs (`#21` — owes the closing tests for 5 "Design fixed" review findings: F-1,
  F-2, F-5, F-8, F-10).
- **Go**: the entire stack (`#22-27`), mirroring Node ticket-for-ticket — including every
  review-driven fix, not the pre-review design. The Go schema must match Node's *current*
  `001_init.sql`, not an earlier version.
- **Frontend**: entire SPA (`#28-30`) — buildable now against the fixed REST contract.
- **`README.md`**: the *final submission* version is still not started for either stack (part of
  `#21`/`#27`). The current root `README.md` is an interim orientation doc, explicitly marked as
  such, and now also carries the `reqs not read` omission note and the AI-usage/transcripts
  disclosure.
- **Primary-build designation**: can't be decided until both stacks have a working `make load`
  test to measure (see `DECISIONS.md`, "Submission").
- **C-2 (time spent) in `REVIEW.md`'s checklist**: still needs the user's answer.

## Known issues / gotchas for future sessions

- **Port 5433 was already taken** by an unrelated local project when Node's Postgres was set up
  — Node uses **5532** instead. Check port availability with `lsof` before assuming a "standard"
  port is free; this will likely recur when Go's docker-compose is created.
- **Express 4 async error handling**: routes must use `src/lib/asyncHandler.ts` or a thrown
  error in an async handler hangs the request instead of producing a 500.
- **`SECRET_ENCRYPTION_KEY` is required, no fallback** (`node/src/config.ts`) — must be a
  base64-encoded 32-byte key (`openssl rand -base64 32`); the app throws on startup if missing
  or the wrong length. `.env.example` has a working local-dev key; generate a real one for
  anything beyond that.
- **Mermaid's sequence-diagram grammar breaks on `&lt;`/`&gt;` HTML entities and mid-message
  semicolons** — discovered the hard way validating `ARCHITECTURE.md`'s diagrams against the
  real parser. Use literal `<text>` or parentheses, and avoid semicolons inside a message; a
  trailing semicolon at end-of-line is fine.
- **`npm audit` flags 5 vulnerabilities** in `node/`, all transitive dev-dependencies of
  `vitest`'s bundled `esbuild` dev server (not production code, not shipped). Not worth fixing
  unless it starts blocking something.
- **Working tree has uncommitted changes** as of this note — see `git status`. Commit before
  starting new work if picking this up fresh, to avoid mixing new changes with this session's.
