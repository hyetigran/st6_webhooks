# System Patterns

The recurring mechanisms this codebase is built from. Full structural detail in
`ARCHITECTURE.md`; full rationale and alternatives-considered in `DECISIONS.md`. This file is
the quick-recall version — read it before writing code that touches any of these.

## The queue is Postgres, not a broker

`SELECT ... FOR UPDATE SKIP LOCKED` on ordinary tables is the queue. No external broker, no
outbox pattern — the events and the queue share one transactional store, so durable-ack and
atomic-fan-out are just transactions. This is the load-bearing decision everything else assumes.

## One in-flight delivery per endpoint, via a lock, not a partition

An endpoint's `busy` column is the lock. A worker claims it with a *short-lived* row lock (held
only for the claim decision, never for the outbound HTTP call), sets `busy = true`, releases the
lock, makes the call outside any transaction. Order within an endpoint's queue is the
`deliveries` table's own insertion order — nothing separately sequenced. **If you're writing
code that creates delivery rows** (expansion, replay), the insertion order you use *is* the
delivery order — get it right or ordering silently breaks.

## Leases, not a reaper

`busy_since` plus a duration derived from the HTTP client's own timeout is the whole crash-
recovery story. There is no background process scanning for dead workers — the same query a
worker uses to find its next job also reclaims stale ones. Don't add a reaper; the pattern is
deliberately reaper-free.

## Tenant fairness is a sort order, not a rate limiter

The claim query's `ORDER BY` puts the least-recently-served tenant first. No token buckets, no
per-tenant concurrency caps, no separate fairness subsystem — it's one column
(`tenants.last_served_at`) and one clause.

## Status fields carry meaning other systems would use a state machine library for

`events.status` (pending_expansion/expanded) exists specifically so a client polling right after
publish doesn't mistake "not expanded yet" for "dropped." `endpoints.status`
(active/paused/halted) unifies two PRD concepts (pause/resume, halt-at-ceiling) into one enum
because they're mutually exclusive in practice. `deliveries.state` plus a read-time computation
(not a stored column) is how "Blocked, waiting on delivery X" gets shown — it's derived from the
endpoint's current head, not persisted.

## Replay is not a special case

A replay is just new rows in the same table, going through the same claim mechanism as live
traffic. This is why a big replay is *allowed* to delay an endpoint's live deliveries — it's the
same queue, FIFO, no priority lane.

## The two backends must produce the same HTTP surface

Node and Go don't just share a database schema — they share a REST contract (routes, auth,
pagination, idempotency semantics) byte-for-byte, because one frontend targets either one. If
you change a response shape in one stack, the other stack and the frontend both need to follow.
