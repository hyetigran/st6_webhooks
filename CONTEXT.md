# Webhook Delivery Service

A service that reliably delivers events a tenant publishes to the HTTP endpoints that tenant has
registered, with retries, ordering, replay, and full visibility into what happened.

## Language

**Event**:
A fact a tenant published, exactly once, regardless of how many endpoints are subscribed to it.
_Avoid_: Message, notification.

**Delivery**:
The obligation to get one Event to one Endpoint. One Delivery can span many Attempts.
_Avoid_: Job, task.

**Attempt**:
One HTTP try at fulfilling a Delivery. A Delivery has one-or-many Attempts; an Attempt always
belongs to exactly one Delivery.
_Avoid_: Try, retry (retry is the *act*, not the noun for a single try).

**Endpoint**:
The registered, stateful target a tenant wants Events delivered to — has a URL, a status, a
signing secret, and a Delivery queue. "Webhook" is not a synonym for this; it names the general
mechanism, never a specific registered target.
_Avoid_: Webhook (as a noun for the registered thing), target, subscriber.

**Claim**:
The momentary, transactional act of a worker acquiring ownership of an Endpoint's next Delivery
via a short-lived row lock. Claiming *produces* a Lease; it isn't the Lease itself.
_Avoid_: Lock (that's the SQL mechanism the Claim uses, not the domain concept), acquire.

**Lease**:
The time-bounded, reclaimable hold a worker has on an Endpoint after claiming it — valid until
released (the Attempt resolves) or until it goes stale and another worker reclaims it. There is
no separate process watching leases; reclaim is passive, folded into the ordinary claim query.
_Avoid_: Lock (see Claim), timeout.

**Tenant**:
The account boundary — owns Endpoints, publishes Events, and is the unit fairness (least-
recently-served ordering) applies to. "Customer" is fine in prose written for a human reader, but
this is the one domain noun; don't let the two words drift into meaning different things.
_Avoid_: Customer, account, org (as domain nouns — "customer" is fine only as informal prose).

**Halted**:
An Endpoint-level status reached when a Delivery exhausts its attempt ceiling. Requires an
explicit operator action (Resume) to leave — never clears itself on its own. Not "terminal" —
that word is reserved for a Delivery's `failed`/`succeeded` states, which have no exit at all;
Halted always has exactly one way out.
_Avoid_: Stuck, dead, disabled, terminal (see Delivery's `failed`/`succeeded` instead).

**Blocked**:
A Delivery-level description, computed at read time (not stored): this Delivery isn't its
Endpoint's current head, so it hasn't been attempted yet. Not terminal — resolves on its own once
the head clears. An Endpoint can have Blocked Deliveries while perfectly healthy; Halted is a
different, worse thing entirely.
_Avoid_: Stuck, waiting (too vague — Blocked specifically means "behind the head").

**Expansion**:
The worker step that turns one Event into its per-Endpoint Deliveries — claims the Event, inserts
one Delivery row per subscribed Endpoint, flips the Event to `expanded`. "Fan-out" is acceptable
loose prose for the resulting set of Deliveries; Expansion is the one precise term for the
mechanism itself.

**Replay**:
A Tenant-initiated request to re-deliver an Endpoint's past Events over a chosen time range. A
Replay is not a new Event — it creates new Deliveries that reuse the original Event's identity,
inserted into the same per-Endpoint Queue live traffic uses. That's what lets a Receiver treat a
replayed Delivery as a legitimate duplicate of something it may have seen before, and it's why a
large Replay can delay that Endpoint's live Deliveries by design — there's no separate lane.

**Queue**:
Always scoped to one Endpoint: the ordered set of its pending Deliveries. This is the scope that
matters for ordering, fairness, and Replay. The system-wide mechanism that implements every
Endpoint's Queue (Postgres, `FOR UPDATE SKIP LOCKED`) is "the transactional queue" or "the
storage layer" — never bare "the queue," to avoid conflating the two scopes.
_Avoid_: Partition, partition head (`PRD.md`'s R-11/R-22/§8 predate this glossary and use
"partition"/"partition head" for this exact concept — treat as the same thing when reading
those sections, but Queue is the term going forward).

**Receiver**:
The actual external HTTP server on the other end of an Endpoint's URL — the thing that can be
slow, down, or buggy in ways our own state has no visibility into. Distinct from Endpoint: the
Endpoint is *our* record (URL, status, secret); the Receiver is *theirs*. The receiver contract
(signing, timestamp tolerance, dedupe-on-Event-id) is a set of promises made to the Receiver, not
a property stored on the Endpoint row.
_Avoid_: Endpoint (as a synonym — Endpoint is our record, Receiver is the real system), target,
subscriber, server.
