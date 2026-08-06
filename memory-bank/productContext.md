# Product Context

Why this exists and who it's for. Full text in `PRD.md` §1-4; this is the compressed version.

## The problem

We send events to customers' HTTP endpoints. Customers report two failures: deliveries are lost
when their endpoint has an outage, and when something goes wrong they can't tell what happened.
Both are visibility and durability problems, not throughput problems — the volume is modest, the
trust deficit is not. Support currently answers "did event X reach us?" by reading application
logs. That's the cost this service removes.

## Users

| User | Needs |
|---|---|
| Integrating developer | Register an endpoint, know the payload contract, verify authenticity, debug a failure without contacting us |
| Support engineer | Answer "what happened to this event" in one screen, without database access |
| Internal publishing service | Publish an event, get a fast durable acknowledgement, never think about delivery |

The **delivery detail** UI screen exists specifically for the support-engineer user — if they
can't answer "what happened to this event" there without asking an engineer, the product has
failed. This is the design center for the visibility work.

## Goals

- No acknowledged event is ever lost, including through process death and receiver outage.
- A customer can explain any delivery outcome from the UI alone.
- One misbehaving receiver or noisy tenant cannot degrade delivery for anyone else.
- Events reach a given endpoint in the order they were published.

## Non-goals

- Not a general message bus — outbound HTTP only.
- Not exactly-once — at-least-once, with what receivers need to dedupe (`event_id`).
- No customer-authored transforms, filters, or payload templating.
- No multi-region or cross-region ordering guarantees.

## Scope for this submission (v0.2.0)

`PRD.md` §9 names what's deliberately deferred to later stages (external broker, CQRS, payload
offload, distributed tracing, etc.) and §10 names three things left genuinely open at write time
(backoff tuning, halt notification, retention policy) — all three were resolved during planning
and now live in `DECISIONS.md` and the map's Out of scope section.
