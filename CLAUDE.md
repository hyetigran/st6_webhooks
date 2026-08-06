## Memory bank

This project's context doesn't survive between sessions on its own — read every file in
`memory-bank/` **before** starting any non-trivial task, in this order: `projectbrief.md` →
`productContext.md` → `systemPatterns.md` → `techContext.md` → `activeContext.md` →
`progress.md`. They're short by design; skipping them costs more time than reading them.

At the end of a session that changes what's built, what's in progress, or what's next, update
`activeContext.md` and `progress.md` to match. If they drift from reality, the wayfinder map
(GitLab issue #1) and `git log` are the tiebreakers — fix the memory bank to match them, not the
other way around.

See also `ARCHITECTURE.md` (structural reference) and `DELIVERABLES.md` (what CASE_STUDY.md
requires and current status).

## Agent skills

### Issue tracker

Issues live in this repo's GitLab Issues (via the `glab` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

Default five canonical roles, used as-is. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context — `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
