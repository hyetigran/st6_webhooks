# Project Brief

Read this first — it's the foundation every other memory-bank file builds on.

## What this is

An engineering take-home assessment (`CASE_STUDY.md`). Project 1 of five offered — **Webhook
Delivery Service** — is the one chosen. Scope for the actual build is `PRD.md`, v0.2.0.

## The meta-decision that shapes everything

This project builds **two full, architecturally-identical implementations** — Node.js/TypeScript
and Go — instead of one, specifically to judge that language choice on measured behavior rather
than argue it in the abstract. That decision (and everything downstream of it) lives in
`DECISIONS.md`. It roughly doubles the work against the brief's 2-5 day timebox; that trade-off
was made deliberately and is tracked, not hidden.

Both builds ship in one repository. One gets designated primary based on measured load-test
performance once both exist (see `DECISIONS.md`, "Submission").

## Source documents, in reading order

1. `CASE_STUDY.md` — the assessment brief (pre-existing, not written by any agent session).
2. `PRD.md` — product requirements for the webhook service itself (pre-existing).
3. `DECISIONS.md` — every architecture decision made resolving PRD.md's open questions, plus
   the two-implementation and shared-frontend decisions. ~2 pages, submission format.
4. `ARCHITECTURE.md` — structural reference: components, data model, request flows.
5. `DELIVERABLES.md` — what CASE_STUDY.md requires and current status against it.

## Where planning history lives

This project was planned using the `wayfinder` skill — a shared map is a GitLab issue (issue #1
in this project's tracker) with child issues as tickets, one per decision or build task. That
map is the authoritative record of *how* each decision in `DECISIONS.md` was reached and what
implementation work remains. `docs/agents/issue-tracker.md` has the GitLab conventions used.

## Repo layout

See `ARCHITECTURE.md`'s "Directory layout" section — `node/`, `go/`, `frontend/` hold the three
buildable pieces; `go/` and `frontend/` don't exist yet.
