# Delivery connections resolve-validate-pin; no redirects followed

R-2's private/loopback/link-local rejection and R-16's "bounded redirect policy" sound like
SSRF defenses but don't actually close the class of attack they're meant to stop, as drawn.
Two gaps: (1) "re-validated at delivery time" reads as validate-then-connect — if the DNS
lookup used to validate is a separate step from the DNS lookup the HTTP client performs to
actually connect, an attacker can rebind the hostname to a private address in the gap between
them (the validated address and the connected-to address are never guaranteed to be the same
one). (2) R-16's redirect bound only limits *count*, not *destination* — a registered URL that
302s to `169.254.169.254` sails through a hop-count limit untouched. Found during an
adversarial review (`REVIEW.md` F-10). Registration-time validation
(`node/src/validation/url.ts`) already does the right thing for *its* purpose — resolves every
A/AAAA record, checks each one — this decision is specifically about the delivery-time
connection, which doesn't exist yet (the delivery worker is ticket #18).

**Decision:** the delivery worker resolves the endpoint's hostname once, validates every
resolved address against the shared denylist, and **pins the validated IP** for the actual TCP
connection (a custom dial/lookup in the HTTP client, with `Host`/SNI left as the original
hostname for TLS and vhost routing) — so the address that gets checked and the address that
gets connected to are provably the same one, closing the rebinding gap. Redirects are **not
followed at all**: the response is treated as the terminal outcome regardless of a `3xx`
status. This is the simpler of two options and matches how major webhook senders behave
(`COMPARISON.md`'s research: neither Stripe nor GitHub follows redirects). The registration-time
and delivery-time checks share **one denylist** (RFC1918, `127/8`, `169.254/16`, `0.0.0.0/8`,
`100.64/10`, plus IPv6 `::1`, `fc00::/7`, `fe80::/10`, `::ffff:0:0/96` v4-mapped forms) instead
of two independently-maintained lists that could drift apart.

## Considered options

- **Follow redirects with resolve-validate-pin re-run at every hop** — considered, rejected:
  more permissive for a receiver doing a legitimate URL migration, but multiplies the surface
  area where the SSRF defense has to be implemented correctly — one hop handled right and the
  next handled wrong is a real failure mode a "don't follow redirects" rule doesn't have.

## Consequences

A receiver that wants to move its webhook URL must re-register with the new URL rather than
relying on an HTTP redirect — a minor integration inconvenience, stated explicitly in the
receiver contract. Both stacks (Node, Go) must implement the same custom-dial pin and share
the same denylist values for the contract to hold identically.
