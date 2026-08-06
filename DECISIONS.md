# Decisions

Two architecturally-identical implementations of the webhook delivery service (v0.2.0 scope,
see `PRD.md`) — Node.js/TypeScript and Go — are being built in parallel so the language choice
can be judged on measured behavior, not argued in the abstract. Both share every decision
below. This is the compressed record; anything hard to reverse, surprising without context, or
the result of a real trade-off gets its own file in `docs/adr/` — cited inline below.

**Current status** (`DELIVERABLES.md` has the live breakdown): the Node stack's endpoint-
management API is built, verified, and adversarially reviewed (`REVIEW.md`, 17 findings, all
resolved). Delivery, replay, visibility, the Go stack, and the frontend are not yet built. The
architecture below is the target, not yet a finished, measured comparison.

## Platform

**Both stacks, not one.** Neither "Node is faster to build correctly" nor "Go has better
multi-core/GC behavior" was conclusive without data, so both are being built — real evidence
over a coin flip. Roughly doubles the build's scope against the timebox (accepted risk, see
"Submission").

**PostgreSQL is the transactional queue, no external broker.** `FOR UPDATE SKIP LOCKED` gives
durable ack, per-endpoint locking, and lease-based crash recovery as plain transactions — an
external broker would need a transactional outbox to get what the shared DB already provides.
Separate API and worker processes, isolated Postgres instance, per stack.

## Ordering, concurrency, and fairness

**Per-endpoint ordering via a `busy` flag**, not a worker-per-partition scheme: a short-lived
row lock (not held for the HTTP call) claims an endpoint's oldest pending delivery; order is
natural insertion order, no sequence counter. `Blocked` is computed at read time, not stored.

**Crash recovery is a passive, fenced lease** (`docs/adr/0002`). `busy_since` turns a claim
into a lease, reclaimed by the same query — no reaper. A `lease_id` token fences every
post-HTTP-call write against a *stalled* (not dead) worker waking up after losing its lease —
`kill -9` never needed this, only a stall does. At-least-once accepted throughout.

**Expansion is serialized per tenant** (`docs/adr/0001`) — naive parallel claiming let two
events for one endpoint expand out of publish order. Fix: a monotonic `events.seq`, expansion
claims a tenant's oldest pending event under `pg_try_advisory_xact_lock(tenant_id)`
(auto-released on commit/crash, still no lease). Cross-tenant expansion stays parallel.

**Tenant fairness is one rule with a stated bound, not a concurrency cap** (`docs/adr/0004`).
Claim query orders by least-recently-served tenant, then oldest pending delivery. This rations
*claims*, but the scarce resource under a slow-receiver attack is *worker-seconds* — the actual
bound: a quiet tenant's added latency is roughly one outbound-timeout cycle, regardless of a
tarpit tenant's endpoint count, since the next claim always goes to whoever's least recently
served. No cap added; a cap remains the documented escape hatch if this bound ever proves
insufficient.

## Publishing, expansion, and replay

**Publish and fan-out are decoupled** via an explicit `events.status` field
(`pending_expansion`/`expanded`) rather than hoping expansion stays fast. Publish inserts one
row, returns `202` immediately; the shared worker pool expands it later in one transaction — no
lease needed, expansion has no external I/O. Fan-out is a subscription snapshot at expansion
time; late subscribers rely on replay, not automatic backfill.

**Replay mirrors publish exactly — same async pattern, same queue** (`docs/adr/0005`). The
original synchronous design had two bugs: O(window) request latency, and a crash between the
idempotency insert and the delivery inserts that could silently return `202` with zero
deliveries created. Fix: `replays` gets the same `status`/async-expansion treatment as events.
The window selects every *resolved* delivery (succeeded or failed), excluding
`pending`/`in_flight` ones — replaying an unattempted delivery would be pure duplication.
Replayed rows reuse the original `event_id`, land in the endpoint's normal queue (no new
locking), and can delay that same endpoint's live traffic by design — no priority lane.

## Receiver contract

HMAC-SHA256 over `"{timestamp}.{raw_body}"`, dedicated headers (`Webhook-Id/Event-Id/Attempt/
Timestamp/Signature`, no `X-` prefix), 5-minute timestamp tolerance. The metadata headers sit
outside the signed content by choice — matches Stripe, TLS already protects transit integrity.

**Secret rotation is sender-side multi-signing** (`docs/adr/0003`) — reversed from the original
call, which put the burden on receiver-side dual-checking. That has a bootstrapping gap: a
receiver can't check a secret it doesn't have yet, so rotation would silently halt endpoints
until the customer's deploy caught up. Now the sender signs with every secret still inside its
rotation-overlap window (current + secondary), so a receiver on either one verifies throughout.

**Delivery connections resolve-validate-pin; no redirects followed** (`docs/adr/0006`).
"Re-validated at delivery time" alone leaves a DNS-rebinding gap between the validate step and
the connect step; the fix pins the exact validated IP for the connection itself. Redirects
aren't followed at all — a bounded hop *count* doesn't bound redirect *destination*, and not
following any is simpler than re-validating at every hop. One denylist, shared between
registration- and delivery-time checks.

**Credentials are hashed or encrypted, never plaintext.** Tenant API keys: SHA-256 (fast hash —
correct here, since the key is high-entropy and unguessable, unlike a password). Signing
secrets must stay recoverable for HMAC, so they're AES-256-GCM-encrypted at rest instead of
hashed. Both fixed in already-shipped code and verified live against Postgres, not just
designed.

## Retries, halting, and visibility

**Global-only backoff for v0.2.0** (per-endpoint tuning is a stated future concern in the PRD,
not current scope). Full-jitter exponential: 1s base, 2x, 30s cap, 6 attempts, env-configurable.
Halt fires immediately on the final failure, not a later claim. Worst case before halt is
**~91s**, not the ~61s an earlier draft stated — 5 backoff gaps (~31s ceiling, ~15.5s expected
with jitter) *plus* up to 6× the outbound timeout if every attempt actually times out. Endpoints
get one `status` enum (`active`/`paused`/`halted`); only `active` ones are claimable. Proactive
halt notification is deferred (the UI already covers "discover by looking"); attempt-history
retention is unbounded.

**Resume states its consequence precisely.** A `failed` delivery is terminal and never
reclaimed — only replay would retry it, at the tail, out of order. `resume`'s response names
the specific deliveries this leaves behind (`skipped_failed_delivery_ids`), not just a count,
and does so identically regardless of which status the endpoint resumes from — no path to
`active` skips the disclosure. Verified live: both the direct and the two-step resume path
return identical results.

**One shared React frontend**, not two, against an identical REST API on both backends (Bearer
key per tenant, `Idempotency-Key` for publish/replay, cursor pagination) — a real timebox lever
given the doubled backend scope. Polling (2–5s), not push; nothing here needs real-time.

## Stretch goals: satisfied, reinterpreted, or cut

Named explicitly, since the brief grades ambiguity-resolution, not just outcomes.

- **Per-endpoint ordering without cross-endpoint blocking, under load** — satisfied.
- **Noisy-neighbor fairness under load** — satisfied, with a stated bound.
- **Replay without disturbing live delivery or creating duplicates** — reinterpreted:
  "live delivery" means *other* endpoints, not the replayed one's own queue; "duplicates" means
  receiver-detectable via the reused `event_id`, not literally impossible.
- **Automatic backoff-then-self-recovery without dropping events** — cut. Auto-resuming under a
  strict ordering guarantee forces skipping the failed head forever or retrying it indefinitely,
  so recovery is an explicit operator action instead. Events are retained, never dropped; a
  production middle path (auto-resume, failed head parked, notification) was consciously not
  built here.

## Submission

Both implementations ship in one repository; one is explicitly designated primary by measured
`make load` performance, tie-broken on code quality — the actual empirical question that
justified building two, submitted as evidence rather than discarded once a winner is picked.

**Risk owned in writing:** doubling backend scope against a 2-5 day timebox is the choice most
likely to read as scope inflation if Go ships thin, and this brief weighs "builds less but
reasons well" over "builds more, carelessly." Accepted fallback, decided now: if Go isn't at
meaningful parity by the timebox's midpoint, ship Node alone and keep this document's Platform
section as design analysis rather than a completed comparison.

## Deliberately out of scope

External broker/outbox, CQRS, payload offload, per-endpoint ordering opt-out, automatic halt
recovery, distributed tracing, per-endpoint retry tuning, proactive halt notifications,
attempt-history retention policy, self-service tenant onboarding, API key scopes (one Bearer
key per tenant can publish, rotate secrets, and resume endpoints — no privilege separation),
and the cloud/IaC bonus (the brief itself calls it "genuinely optional"; Docker Compose already
covers local reproducibility). Each is a deliberate cut — `docs/adr/` and the project's issue
tracker hold fuller rationale where needed.

## What I'd do differently with more time

Build the noisy-neighbor load test first and let it drive the fairness mechanism, rather than
the reverse. Re-check the shared-frontend contract once both backends' real response shapes
exist — it was fixed before either implementation was. Widen the retry window before any real
traffic: `COMPARISON.md`'s sourced comparison against Stripe/Shopify/GitHub shows our ~91s
window is roughly two to three orders of magnitude shorter than production systems' — tuned for
fast, observable tests, not production realism. The values are already env-configurable, so
this is a default to revisit, not a redesign.
