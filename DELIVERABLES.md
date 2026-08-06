# Deliverables

What `CASE_STUDY.md` requires, and where this project stands against it. This file tracks the
_static_ requirements — for live, ticket-level status, see the wayfinder map (GitLab issue #1).
That's the source of truth; this file exists so "what do we actually owe them" doesn't have to
be reconstructed from the brief every time.

## What CASE_STUDY.md requires

> 1. **The application**, meeting the requirements in your brief.
> 2. **`DECISIONS.md`** — about two pages, no more. Your key architectural choices, the
>    alternatives you considered and rejected, the trade-offs you accepted, and what you'd do
>    differently with more time. Call out anything you deliberately left out of scope.
> 3. **`README.md`** — enough for us to run it. For our intake tracking, add a line at the very
>    top of the file that reads exactly: `reqs not read`.

Submission is one repository (or a link to it), including `DECISIONS.md` and `README.md`.

## Status

| Deliverable                       | Status                                                                                                                                                                                                                                                                                  |
| --------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `DECISIONS.md`                    | **Done.** Written, ~1050 words, at repo root.                                                                                                                                                                                                                                           |
| `ARCHITECTURE.md`                 | Done (this project's own addition, not required by the brief).                                                                                                                                                                                                                          |
| The application — Node backend    | In progress. Endpoint management API done and verified; publish/expansion, delivery worker, replay, visibility API, tests/deploy remain. See map issues #16-21.                                                                                                                         |
| The application — Go backend      | Not started. See map issues #22-27.                                                                                                                                                                                                                                                     |
| The application — shared frontend | Not started. See map issues #28-30.                                                                                                                                                                                                                                                     |
| Primary-build designation         | Pending — decided by measured `make load` performance once both backends exist (see the map's "Comparison/decision criteria" ticket, #14).                                                                                                                                              |
| `README.md`                       | Interim orientation version done at repo root (development-stage, not the submission deliverable). The real one — clone-to-run, under 15 minutes — is owned by the "Test suite & deployment" ticket in each stack (#21 Node, #27 Go); the primary build's version supersedes this file. |

## Scope beyond the brief

Building two full implementations instead of one, and comparing them, was this project's own
choice (see `DECISIONS.md`'s Platform section) — not something `CASE_STUDY.md` asked for. It
roughly doubles the work against the brief's 2-5 day timebox; that trade-off is accepted and
recorded on the map, not hidden.
