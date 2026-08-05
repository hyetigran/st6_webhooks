# Decisions

Two architecturally-identical implementations of the webhook delivery service (v0.2.0 scope,
see `PRD.md`) — one in Node.js/TypeScript, one in Go — built in parallel so the language choice
could be judged on measured behavior rather than argued in the abstract. Both share every
decision below; only the runtime differs. Full reasoning and alternatives live on the project's
issue tracker (wayfinder map); this is the compressed record.

## Platform

**Both Node.js/TypeScript and Go, not one.** Considered: Node alone (one language end-to-end,
faster to build correctly) or Go alone (better multi-core scheduling, more predictable GC
pauses). Neither argument was conclusive without data, so both got built — real evidence over
a coin flip. Trade-off accepted: this roughly doubles the build's scope against the assessment's
timebox.

**PostgreSQL as the transactional queue, no external broker.** `SELECT ... FOR UPDATE SKIP LOCKED`
gives durable ack, per-endpoint locking, and lease-based crash recovery as plain transactions.
An external broker would need a transactional outbox to get the same guarantee the shared DB
already provides for free — added complexity with no offsetting benefit at this scale (deferred
to a later stage per `PRD.md` §9). Separate API and worker processes per stack, isolated Postgres
instance per stack, Docker Compose locally.

## Ordering, concurrency, and fairness

**Per-endpoint ordering via a `busy` flag**, not a dedicated worker-per-partition scheme. A
worker claims an endpoint's oldest pending delivery through a short-lived row lock (not held for
the HTTP call itself), sets `busy = true`, and any worker in the shared pool can pick up the next
endpoint. Delivery order is the table's natural insertion order — no separate sequence counter,
though this makes the async-expansion step responsible for preserving publish order per endpoint.
`Blocked` status is computed at read time from the endpoint's current head, not persisted, since
only the head can ever be in-flight.

**Crash recovery is lease-based and passive**, folded into the same claim query rather than run
by a separate reaper process. Lease duration is derived from the HTTP client's total timeout
(`timeout + max(timeout, 30s)`) rather than an independent constant, so the two numbers can't
drift apart. A reclaimed delivery's orphaned attempt gets a synthetic terminal outcome
(`worker_lease_expired`) before the retry starts, keeping the attempt history honest. At-least-once
delivery is accepted — a dead worker's request may have already reached the receiver — consistent
with the receiver contract's dedupe requirement, not something engineered around.

**Tenant fairness is one rule, not a concurrency cap.** The claim query orders candidates by
least-recently-served tenant first, endpoint's oldest pending delivery second, updating the
tenant's timestamp in the same transaction that claims the delivery. This gives both
no-tenant-overconsumes and no-tenant-starves from a single mechanism — no separate cap to
configure or keep in sync with worker-pool size.

## Publishing, expansion, and replay

**Publish and delivery fan-out are decoupled**, with an explicit `events.status` field
(`pending_expansion` / `expanded`) rather than hoping expansion stays fast. Publish inserts one
row and returns `202` immediately, independent of subscriber count. A shared worker pool (the
same one that claims deliveries) later expands an event into per-endpoint delivery rows inside
one atomic transaction — no lease needed, since expansion has no external I/O to get stuck on.
Fan-out is a snapshot of subscriptions at expansion time; an endpoint that subscribes later
doesn't retroactively receive past events — that gap is what replay is for.

**Replay reuses the same queue, not a side channel.** A replay creates new delivery rows (fresh
`delivery_id`) referencing the original `event_id`, inserted into the endpoint's normal queue.
No new locking is needed — the existing ordering mechanism already serializes them, which is why
a large replay is allowed to delay that endpoint's live deliveries by design. Replay selects
every delivery in the chosen window regardless of original outcome (not just previously-failed
ones), and is idempotent on a caller-supplied key via the same pattern used for publish.

## Receiver contract

HMAC-SHA256 over `"{timestamp}.{raw_body}"`, carried in dedicated headers with no legacy `X-`
prefix (`Webhook-Id`, `Webhook-Event-Id`, `Webhook-Attempt`, `Webhook-Timestamp`,
`Webhook-Signature`), 5-minute timestamp tolerance. Secret rotation has no key-id field — the
sender always signs with whatever secret is currently active, relying entirely on the receiver's
overlap-window dual-check rather than adding sender-side versioning the spec didn't ask for.

## Retries, halting, and visibility

**Retry backoff is global-only for v0.2.0**, not per-endpoint — the PRD's own framing ("likely
needed once real customers have opinions") reads as a future concern, not a current one.
Full-jitter exponential backoff: 1s base, 2x multiplier, 30s cap, 6 attempts (~61s worst case
before halt), all env-configurable so tests can run fast without picking artificially small
production defaults. Halted endpoints get a `status` enum (`active`/`paused`/`halted`) unifying
pause/resume with halt-at-ceiling; proactive notification (email/Slack) is deferred, since the
UI's health screen already satisfies "discover by looking." Attempt-history retention is
unbounded — no realistic growth risk within this submission's lifetime.

**One shared React frontend**, not two. Both backends expose an identical REST API (Bearer
API-key auth per tenant, `Idempotency-Key` header for publish/replay, cursor-based pagination)
so a single SPA can point at either one — a real timebox lever given the doubled backend scope.
The UI polls every 2–5s rather than pushing over WebSockets/SSE; nothing in the visibility
requirements demands real-time, and it avoids building connection-lifecycle logic twice.

## Submission

Both implementations ship in one repository; one is explicitly designated primary based on
measured `make load` performance (the noisy-neighbor fairness test), tie-broken on code
quality if performance is comparable. This was the actual empirical question that justified
building two in the first place — the comparison is submitted as evidence, not discarded once
a winner is picked.

## Deliberately out of scope

External broker/outbox, CQRS read models, payload offload to object storage, per-endpoint
ordering opt-out, automatic recovery from a halt, distributed tracing, per-endpoint retry
tuning, proactive halt notifications, attempt-history retention policy, self-service tenant
onboarding. Each is a deliberate cut, not an oversight — full rationale per item is on the
issue tracker.

## What I'd do differently with more time

Build the noisy-neighbor load test first and let it drive the fairness mechanism, rather than
designing fairness then writing the test after. Consider whether the shared-frontend decision
still holds once both backends' actual response shapes exist — the contract was fixed before
either implementation did, so drift is possible. Revisit per-endpoint retry tuning and a
retention policy for real (not demo) traffic.
