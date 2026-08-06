# Fence post-claim writes with a lease_id token

The original lease design (`busy`/`busy_since`, ADR-003's underlying decision) handles a
*dead* worker correctly — a killed process never writes back, so reclaim is safe. It does not
handle a *stalled* worker: a paused event loop (GC, container CPU throttling, `SIGSTOP`) means
the worker's own timers don't fire either, so its outbound request can still be outstanding
after its lease has already expired and been reclaimed. If that worker later wakes and
unconditionally writes its attempt outcome, it can overwrite state a second worker already
wrote, and briefly leave two workers believing they own the same Endpoint's in-flight slot —
violating the one-delivery-in-flight invariant (ADR-002) and corrupting attempt history
(R-15). Found during an adversarial review (`REVIEW.md` F-2); `kill -9` chaos testing cannot
surface this bug, since a dead process never reaches the write-back step — only a stall does.

**Decision:** every post-claim write that happens *after* the outbound HTTP call — the
attempt-outcome update, the delivery-state update, and the `busy` release — is fenced against
a `lease_id UUID` captured at claim time. `endpoints` gains a `lease_id` column, set fresh
(`gen_random_uuid()`) alongside `busy`/`busy_since` at claim. The write-back happens in one
transaction that first confirms the endpoint's current `lease_id` still matches the one
captured at claim; if it doesn't (another worker has since reclaimed and is now the true
owner), the entire write-back is dropped silently — the worker's send may still have reached
the receiver, which is an accepted at-least-once duplicate, but its write no longer corrupts
shared state.

## Considered options

- **Fence on `busy_since` instead of a new column** — considered, and correct in practice
  (two claims of the same endpoint can't land on the same microsecond). Rejected in favor of a
  dedicated token: `lease_id`'s only job is being a fencing value, which is easier to reason
  about and review than overloading a timestamp that also carries "when did this claim start"
  meaning.

## Consequences

One more column, one more value to thread through the worker's claim → send → write-back path
in both stacks (Node and Go must fence identically). No new failure mode is introduced — this
only makes an existing corruption window impossible; the at-least-once duplicate-send
possibility this project already accepts is unchanged.
