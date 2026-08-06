# Serialize event expansion per tenant, not globally parallel

Async expansion (turning a published Event into per-Endpoint Deliveries) was originally
claimed via plain `FOR UPDATE SKIP LOCKED` with no ordering — fully parallel across all
pending events. That breaks per-endpoint publication order (R-11): two Events published to
the same Endpoint can be expanded by different workers in either order, and once a Delivery
is sent there's no repairing the sequence. Found during an adversarial review (`REVIEW.md`
F-1) before any worker code existed to inherit the bug.

**Decision:** add a monotonic `events.seq bigserial` to capture publish order, and serialize
expansion **per tenant** using `pg_try_advisory_xact_lock(tenant_id)` — a worker claims a
tenant's oldest `pending_expansion` Event by `seq`, expands it, and the lock releases
automatically on commit or crash. Expansion across different tenants stays fully parallel;
only expansion *within* one tenant is serialized — which correctly bounds the ordering
guarantee to per-Endpoint, since every Endpoint belongs to exactly one Tenant.

## Considered options

- **Global serialization** — rejected: unnecessarily kills expansion throughput across
  unrelated tenants for no ordering benefit.
- **A stored lease** (the delivery worker's `busy`/`busy_since` pattern) — rejected: an
  advisory transaction lock already gives crash-safe auto-release for free, for the same
  reason ADR-004 originally gave for needing no lease machinery here — expansion has no
  external I/O to get stuck on, so plain transactional semantics suffice.

## Consequences

Expansion throughput is now bounded by one worker at a time *per tenant*, not per system. Fine
at this project's load-test scale; a single tenant publishing extremely high event volume
could become an expansion bottleneck — worth revisiting if that ever becomes a real workload.
