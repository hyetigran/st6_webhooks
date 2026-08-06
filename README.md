# Webhook Delivery Service

A reliable webhook delivery system — register endpoints, publish events, get durable
at-least-once delivery with per-endpoint ordering, retries, replay, and a UI that can answer
"what happened to this event" without reading logs. Built for `CASE_STUDY.md`'s Project 1
brief; scope is `PRD.md` (v0.2.0).

**This repository holds two full, architecturally-identical implementations** — Node.js/
TypeScript and Go — built in parallel to judge that language choice on measured behavior rather
than argue it in the abstract, plus one shared frontend. See `DECISIONS.md` for why.

## Status: in progress

This is a working submission being built incrementally. Not everything below is built yet —
`DELIVERABLES.md` has the current, honest status against what `CASE_STUDY.md` requires.
Briefly, right now: the Node stack's endpoint-management API is built and verified; everything
else (delivery, replay, the Go stack, the frontend) is still open.

**This file will be superseded.** Once both stacks pass `PRD.md` §8's acceptance criteria and
one is designated primary (see `DECISIONS.md`, "Submission"), the primary stack's own
"Test suite & deployment" ticket writes the final submission README — a real clone-to-run guide
under the 15-minute bar `CASE_STUDY.md` asks for. Until then, this README is for orientation.

## Start here

| Doc               | What it's for                                                                                                                                                                                  |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `CASE_STUDY.md`   | The assessment brief (pre-existing, not written by this project).                                                                                                                              |
| `PRD.md`          | Product requirements for the service itself (pre-existing).                                                                                                                                    |
| `DECISIONS.md`    | Every architecture decision, alternatives considered, trade-offs accepted. ~2 pages, the actual submission deliverable.                                                                        |
| `ARCHITECTURE.md` | Structural reference — components, data model, request flows.                                                                                                                                  |
| `DELIVERABLES.md` | What's required and current status against it.                                                                                                                                                 |
| `memory-bank/`    | Fast-orientation context for anyone (human or AI) picking this up cold. Read in the order `projectbrief` → `productContext` → `systemPatterns` → `techContext` → `activeContext` → `progress`. |

Planning and implementation are tracked as a `wayfinder` map on this repo's GitLab issue tracker
(issue #1) — every architecture decision and build ticket has its full reasoning there.

## Repo layout

```
node/        Node.js/TypeScript backend — endpoint management API built, rest in progress
go/          Go backend — not started
frontend/    Shared React SPA — not started
```

## Running what exists today

Only the Node stack's endpoint management API is runnable right now:

```sh
cd node
docker compose up -d postgres
cp .env.example .env
npm install
npm run migrate
npm run seed        # prints a demo tenant's API key
npm run dev
```

See `node/README.md` for details. `go/` and `frontend/` don't exist yet.

## AI usage and transcripts

This project was built with an AI coding agent throughout — architecture decisions, code,
docs, and the adversarial review in `REVIEW.md` were all AI-assisted, directed and reviewed by
the submitter at each step. Session transcripts and prompts are available on request, per
`CASE_STUDY.md`'s offer to read them.
