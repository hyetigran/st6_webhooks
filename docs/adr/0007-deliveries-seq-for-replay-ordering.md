# Add deliveries.seq for reliable same-endpoint insertion order

Replay (`docs/adr/0005`) creates potentially many new delivery rows for one endpoint in a
single atomic transaction, promising they land "in original chronological order." Postgres's
`now()` — used for `deliveries.created_at`'s default — is transaction-stable: every statement
inside one transaction sees the identical value. A multi-row `INSERT ... SELECT` for one
endpoint's replay batch would therefore give every new row the exact same `created_at`. The
claim query and every existing index order strictly by `(endpoint_id, created_at)`, so ties
among same-endpoint rows would be broken arbitrarily by Postgres — not guaranteed to match the
original order at all, silently violating the guarantee. Found while implementing the
"[Node] Replay" ticket, not by the original adversarial review (`REVIEW.md` F-8, which
`docs/adr/0005` already resolves, is about crash-safety and O(window) latency, not this).
Expansion (`docs/adr/0004`) never hits this: one event's expansion inserts at most one delivery
per endpoint, so there's never a same-endpoint tie to break.

**Decision:** add a monotonic `deliveries.seq BIGSERIAL`, mirroring `events.seq`
(`docs/adr/0001`) — the established fix for exactly this class of problem. Every place that
currently relies on `deliveries.created_at` for same-endpoint sequencing (the claim query's
delivery selection, the claimable-candidate tiebreak, the pending-delivery partial index)
switches to `seq` instead. `created_at` is retained for its existing display/audit purposes but
is no longer relied on for ordering guarantees.

## Considered options

- **`clock_timestamp()` instead of `now()` for replay's insert only** — rejected: a narrow fix
  scoped to one insert site that doesn't generalize if a similar same-endpoint bulk-insert need
  arises elsewhere, and it changes `created_at`'s meaning inconsistently (transaction time
  everywhere else in the schema, wall-clock time at this one call site).

## Consequences

One more column (migration `002_deliveries_seq.sql`, since `001_init.sql` was already applied
to real databases by this point in the project — schema changes from here on are additive
migrations, not retroactive edits to the init migration). The claim query and the
claimable-candidate tiebreak in `node/src/worker/delivery.ts` order by `seq`, not `created_at`.
Both stacks (Node, Go) must implement identically.
