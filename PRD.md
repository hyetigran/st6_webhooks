# Webhook delivery service — product requirements

Scope: `v0.2.0`, the tagged submission. Later stages are named in §9 and deliberately unbuilt.
Architectural rationale lives in `DECISIONS.md` (ADR-001 … ADR-008); this document states what
the product must do and how each requirement is demonstrated.

---

## 1. Problem

We send events to customer HTTP endpoints. Customers report two failures: deliveries are
lost when their endpoint has an outage, and when something goes wrong they cannot tell what
happened. Both are visibility and durability problems rather than throughput problems — the
volume is modest, the trust deficit is not.

Support currently answers "did event X reach us?" by reading application logs. That is the
cost we are removing.

## 2. Users

| User                        | Needs                                                                                                       |
| --------------------------- | ----------------------------------------------------------------------------------------------------------- |
| Integrating developer       | Register an endpoint, know the payload contract, verify authenticity, debug a failure without contacting us |
| Support engineer            | Answer "what happened to this event" in one screen, without database access                                 |
| Internal publishing service | Publish an event, get a fast durable acknowledgement, never think about delivery                            |

## 3. Goals

- No acknowledged event is ever lost, including through process death and receiver outage.
- A customer can explain any delivery outcome from the UI alone.
- One misbehaving receiver or noisy tenant cannot degrade delivery for anyone else.
- Events reach a given endpoint in the order they were published.

## 4. Non-goals

- Not a general message bus. Outbound HTTP only.
- Not exactly-once. We deliver at least once and give receivers what they need to dedupe (§6).
- No customer-authored transforms, filters, or payload templating.
- No multi-region or cross-region ordering guarantees.

## 5. Functional requirements

### Endpoint management

- **R-1** Register an endpoint with a URL and a set of subscribed event types.
- **R-2** Registration rejects URLs that resolve to private, loopback, or link-local ranges.
  Re-validated at delivery time, since DNS can change after registration.
- **R-3** Each endpoint has a signing secret, viewable once and rotatable. Rotation supports
  an overlap window so receivers can accept both secrets during a cutover.
- **R-4** An endpoint can be paused and resumed. Paused endpoints accumulate deliveries
  rather than discarding them.

### Publishing

- **R-5** Publish accepts an event type and a JSON payload, returns `202` with an event ID.
- **R-6** Publish is idempotent on a caller-supplied key. A repeated key returns the original
  event ID and creates no new deliveries.
- **R-7** The response is returned only after the event is durably persisted.
- **R-8** Publish latency is independent of subscriber count. Expansion into per-endpoint
  deliveries happens asynchronously (ADR-004).
- **R-9** Event state is visible during expansion, so a customer who publishes and immediately
  looks does not see an event with zero deliveries and conclude it was dropped.

### Delivery

- **R-10** Each event is delivered to every endpoint subscribed to its type.
- **R-11** Deliveries to a single endpoint are attempted strictly in publication order, one at
  a time (ADR-002).
- **R-12** A failing delivery blocks its own endpoint and no other. Deliveries behind it report
  `Blocked` and name what they are waiting for.
- **R-13** Failures retry on exponential backoff with jitter, to a configured attempt ceiling.
- **R-14** At the ceiling, the endpoint halts. Deliveries are retained, not discarded.
  Resuming is an explicit operator action that states the ordering consequence.
- **R-15** Every attempt is recorded before the request is issued, with response status,
  truncated body, duration, and error class captured after.
- **R-16** Requests carry a connect and total timeout, a response body size cap, a bounded
  redirect policy, and a per-host connection limit.
- **R-17** A worker that dies mid-delivery does not strand its work. The delivery returns to
  the queue when its lease expires (ADR-003).
- **R-18** No single tenant can consume more than its share of delivery capacity, and no tenant
  can be starved indefinitely (ADR-007).

### Replay

- **R-19** A customer can replay deliveries for an endpoint over a chosen time range.
- **R-20** Replay reuses original event IDs, so a conforming receiver treats them as duplicates.
- **R-21** Replay is idempotent on a request key — invoking it twice does not double-enqueue.
- **R-22** Replay is serialized through the endpoint's partition, preserving the one-in-flight
  invariant. A large replay delays that endpoint's live deliveries and no one else's (ADR-005).

### Visibility

- **R-23** Every delivery exposes its state, attempt count, next attempt time, last response,
  and — when blocked — the delivery it is waiting on.
- **R-24** Events are searchable by ID, type, endpoint, and time range.
- **R-25** Endpoint health shows queue depth, oldest pending delivery, and recent success rate.

## 6. Receiver contract

Published in `README.md` and enforced by the fixtures in the test suite.

- Each delivery carries a stable `event_id` that never changes across retries or replays,
  a `delivery_id` unique per attempt sequence, and an attempt number.
- Requests are signed with an HMAC over timestamp and body; receivers must verify and reject
  stale timestamps.
- Receivers must be idempotent on `event_id` — specifically, on *successfully processed*
  `event_id`s. An `event_id` recorded as seen at attempt time (before processing succeeds)
  would make a receiver silently no-op the replay of an event that never actually completed —
  defeating the reason to replay it in the first place. We cannot make receivers do this
  correctly, so we document the distinction and give them the identifier to do it with
  (`REVIEW.md` F-13).
- A `2xx` is success. Everything else retries. Receivers should respond before doing work.

## 7. UI surfaces

1. **Endpoints** — list with health, plus create, pause, rotate secret.
2. **Event detail** — the event, its payload, and its fan-out across endpoints.
3. **Delivery detail** — the attempt timeline: what was sent, what came back, why it is
   still outstanding, and when it will next be tried.
4. **Endpoint queue** — ordered pending deliveries for one endpoint, with the head highlighted
   when halted and the resume action alongside its consequence.

The delivery detail screen is the primary one. If a support engineer cannot answer "what
happened to this event" there without asking an engineer, the product has failed.

## 8. Acceptance criteria

Requirements are accepted when the named command passes, not when the code exists.

| Requirement       | Demonstrated by                                                                                     | Command           |
| ------------------ | ------------------------------------------------------------------------------------------------------ | ----------------- |
| R-7, R-17          | Kill a worker mid-delivery; lease expires, work resumes, no event lost                                 | `make chaos`      |
| R-11, R-12         | Fail a partition head; followers report `Blocked`, then drain in order on recovery                     | `make chaos`      |
| R-8, R-11          | Publish E1 then E2 to the same endpoint under concurrent expansion; D(E2) is never delivered before D(E1); per-endpoint delivery order equals `events.seq` order | `make chaos`, `make properties` |
| R-11, R-15, R-17   | `SIGSTOP` a worker mid-request past lease expiry, let another reclaim and complete, `SIGCONT` the first; its write is dropped, exactly one terminal state, no interval with two in-flight attempts | `make chaos`      |
| R-3                | Rotate a secret; receiver verifies against the old secret only throughout the overlap window and deliveries never halt; after expiry, old-secret-only verification fails | `make test`       |
| R-6, R-21          | Repeated publish and replay keys create no additional deliveries                                       | `make properties` |
| R-19, R-21, R-22   | Crash-inject between a replay's durable ack and its expansion; retry the same key; deliveries created exactly once, in original order                | `make properties` |
| R-8                | Publish latency flat from 10 to 10,000 subscribers; expansion completes                                | `make load`       |
| R-8, R-19, R-22    | Large-window replay leaves replay-API latency flat (async expansion, not synchronous)                   | `make load`       |
| R-18               | One tenant floods; a quiet tenant's p99 stays within its bound                                          | `make load`       |
| R-18               | One tenant saturates the worker pool with slow (tarpit) receivers; a quiet tenant's added latency stays within roughly one outbound-timeout cycle | `make load`       |
| R-13, R-14         | Reconstruct the backoff schedule from `attempts` timestamps; matches the stated formula; endpoint halts on the final failure, not a later claim | `make test`       |
| R-15               | Crash after a successful send; event ID identical across both attempts, one terminal state             | `make chaos`      |
| R-2, R-16          | Registration and delivery both reject private ranges; a stub resolver that rebinds mid-flow is rejected at delivery time (resolve-validate-pin, not validate-then-connect); a receiver that 302s to a metadata address is not followed; `::ffff:127.0.0.1` literal rejected; slow-loris receiver times out | `make test`       |

**Not yet named**, pending the tickets that build the relevant surface (accepted by inspection
until then is not sufficient for these — they're behavioral claims, not CRUD — this is a gap,
not a decision): R-4 (paused endpoints accumulate and are never claimed), R-9 (event status
visible during expansion), R-10 (fan-out completeness against all subscribed endpoints), R-20
(receiver dedupe rule doesn't defeat replay of a previously-failed event), R-23–25 (read-surface
correctness: delivery detail, event search, endpoint health). Each needs a `make test`-level API
assertion, not a chaos/load scenario — specify alongside the visibility API (#20) and the dedupe
contract fix (see PRD §6, tracked separately).

**Accepted by inspection, no dynamic test needed:** R-1 (register an endpoint) and R-5 (publish
returns 202 with an event ID) are plain CRUD — their correctness is visible directly in the
route handler, and `make test` already exercises them incidentally via fixtures for the rows
above.

`make verify` runs all of the above. Seeds are logged, time is injected, and artifacts land
in `evidence/` and regenerate in CI.

## 9. Deferred

Named because an unbuilt thing that is named is a decision, and one that is not is an oversight.

| Deferred                                  | Stage | Why not now                                                                                                                                             |
| ----------------------------------------- | ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| External broker and transactional outbox  | 3     | The queue shares a database with the events, so enqueue is already transactional. A broker would create the need for an outbox, not remove it.          |
| CQRS read model, table partitioning       | 4     | Read and write contention is not the current bottleneck.                                                                                                |
| Payload offload to object storage         | 4     | Payloads are capped JSONB.                                                                                                                              |
| Per-endpoint ordering opt-out             | —     | Most receivers do not need ordering and would prefer throughput, but supporting both doubles the delivery path and the test matrix. First thing to add. |
| Automatic recovery from a halted endpoint | —     | Requires a human judgement about ordering by design (R-14).                                                                                             |
| Distributed tracing                       | 4     | Structured logs with correlation IDs are sufficient at this size.                                                                                       |

## 10. Open questions

- Attempt ceiling and backoff schedule are configured globally. Per-endpoint tuning is likely
  needed once real customers have opinions.
- Halted endpoints have no notification path. Customers currently discover the halt by looking.
- Retention of attempt history is unbounded. Needs a policy before this runs for a year.

## 11. Note on the brief

The assessment asks for a line at the top of `README.md` asserting the requirements were not
read. That cannot be written truthfully by anyone who read far enough to find it, so it has
been omitted and flagged here instead. If it is an intake marker rather than a consistency
check, it is a one-line fix.
