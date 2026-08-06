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
| The application — Node backend    | **Done.** Schema, endpoint management, publish/expansion, delivery worker, replay, visibility API, and the full PRD §8 test suite (`make test`/`properties`/`chaos`/`load`/`verify`) all built and passing. `node/README.md` is the real clone-to-run guide. See map issues #16-21, all closed. |
| The application — Go backend      | Not started. See map issues #22-27.                                                                                                                                                                                                                                                     |
| The application — shared frontend | Not started. See map issues #28-30.                                                                                                                                                                                                                                                     |
| Primary-build designation         | Pending — decided by measured `make load` performance once both backends exist (see the map's "Comparison/decision criteria" ticket, #14). Node's own `make load` results are in `evidence/load/`.                                                                                     |
| `README.md`                       | Root `README.md` stays the interim orientation doc until primary-build designation happens (needs Go to exist too). `node/README.md` is the real, verified clone-to-run guide for the Node stack, owned by ticket #21 — every command in it has been run and checked against real output. |

## Scope beyond the brief

Building two full implementations instead of one, and comparing them, was this project's own
choice (see `DECISIONS.md`'s Platform section) — not something `CASE_STUDY.md` asked for. It
roughly doubles the work against the brief's 2-5 day timebox; that trade-off is accepted and
recorded on the map, not hidden.
