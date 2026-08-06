# State and test-cover the actual fairness bound, not just the rule

ADR-007's decision (in the wayfinder map) — order the claim query by least-recently-served
tenant, no per-tenant concurrency cap — rations *claims*, not *worker-seconds*. It was accepted
on the reasoning that it gives "both no-overconsumption and no-starvation," but that framing
doesn't distinguish a tenant flooding with *volume* (well-covered by the existing `make load`
noisy-neighbor test) from a tenant occupying every worker with *slow* receivers — a tarpit
tenant with as many endpoints as there are workers can legitimately hold the entire pool for a
full outbound timeout each, and a quiet tenant then waits for the first one to free. R-18's
"share of delivery capacity" language currently promises more than the single sort-order rule
is tested against. Found during an adversarial review (`REVIEW.md` F-5).

**Decision:** the mechanism doesn't change — no concurrency cap is added. What changes is that
the actual bound gets stated and becomes a test commitment, not just an assumption. The bound:
once a worker frees (any tarpit-tenant call completing or timing out), the fairness rule routes
the *next* claim to whichever tenant has gone longest unserved — a tarpit tenant's
`last_served_at` was just bumped when its workers were claimed, so a quiet tenant with an older
timestamp is served next, not another tarpit-tenant endpoint. That bounds a quiet tenant's
added latency to **roughly one outbound-timeout cycle** (the current default: 10s), regardless
of how many endpoints the tarpit tenant has. A `make load` tarpit scenario is now a stated
requirement for the eventual test-suite ticket (Node #21 / Go #27): saturate the worker pool
with one tenant's slow-responding endpoints, publish from a quiet tenant concurrently, assert
its p99 added latency stays within roughly 1.5x the configured outbound timeout (safety margin
for scheduling jitter).

## Considered options

- **Add a per-tenant concurrency cap** — considered, rejected again (consistent with ADR-007's
  original call): a cap bounds worst-case latency tighter, but needs a live in-flight counter
  and a cap value that may not fit the actual tenant mix, for a bound the existing rule already
  gets close to without that bookkeeping.

## Consequences

The bound is a genuine promise now, not an assumption — the test-suite ticket owes a specific
scenario and a specific assertion, not just "one tenant floods." If a real workload ever needed
a tighter bound than one timeout cycle, the concurrency-cap option above is the documented
escape hatch, not a redesign from scratch.
