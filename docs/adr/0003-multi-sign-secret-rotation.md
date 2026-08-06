# Sender signs with every active secret during rotation

The original rotation design (part of the "Receiver contract: signing & idempotency" decision
in `DECISIONS.md`) had the sender sign only with the endpoint's current secret, relying
entirely on the *receiver* to dual-check both the old and new secret during R-3's overlap
window. That has a bootstrapping gap: the receiver can only dual-check once it actually has
the new secret, and it only gets it at rotation — so every delivery between rotation and the
customer's deploy fails verification. With a several-minutes-to-halt retry window (see the
Attempt ceiling & backoff schedule decision), any human-speed deploy overruns it: a routine
secret rotation silently halts the endpoint. Found during an adversarial review (`REVIEW.md`
F-3), which also noted `COMPARISON.md` had already surfaced the correct pattern in passing —
Stripe signs with every active secret for up to 24 hours specifically so the receiver can cut
over at leisure, at the cost of doing more work on the sender's side.

**Decision:** reverse the original call. `endpoints` gains `secondary_secret` and
`secondary_secret_expires_at`. On rotation, the current `signing_secret` moves to
`secondary_secret` with an expiry set to now + the rotation overlap window (already a
configured value, previously only informational); a fresh secret becomes the new
`signing_secret`. The sender signs each outgoing request once per still-active secret and
sends every resulting signature (comma-separated in `Webhook-Signature`, Stripe-style) — a
receiver that's only caught up to the old secret still verifies successfully, and a receiver
that's already deployed the new one also verifies successfully, for the whole overlap window.

## Considered options

- **Keep receiver-only dual-check, widen the retry window instead** — considered, rejected.
  That reduces the blast radius (more retries before halting) but doesn't fix the actual gap;
  a customer's deploy taking longer than the window would still hit it, just less often.
- **Require the customer to fetch the new secret out-of-band before we cut over** — rejected;
  adds a manual step to what should be an atomic rotation action, and the overlap window
  already exists in the spec precisely so this isn't necessary.

## Consequences

Signing does more work per request (one HMAC computation per active secret, at most two,
only during a rotation window) — negligible cost. "Viewable once" now applies per secret, not
per endpoint. Both stacks must implement multi-secret signing identically for the shared
contract to hold.
