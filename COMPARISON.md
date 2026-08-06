# Comparison: this project vs. Stripe, Shopify, and GitHub webhook delivery

This compares the mechanisms described in `ARCHITECTURE.md` and `DECISIONS.md` against the
publicly documented webhook-delivery behavior of three production systems operating at a scale
this take-home never has to reach. The point isn't to claim parity with Stripe, Shopify, or
GitHub — it's to check which of this project's choices track how real systems actually behave
under the same constraints (at-least-once delivery, no ordering broker, a receiver that can be
slow or down), and which are simplifications that make sense only because of the assessment's
scope. Every vendor claim below is sourced to that vendor's own developer documentation (or, where
noted, a vendor-owned engineering/changelog post treated as secondary-but-first-party); anywhere a
vendor doesn't publish something, this says so instead of guessing.

## Comparison table

| Dimension | Ours | Stripe | Shopify | GitHub |
|---|---|---|---|---|
| **Ordering** | Strict FIFO per endpoint, enforced by the sender via a `busy` row lock — one delivery in flight per endpoint | Explicitly not guaranteed, any order | Explicitly not guaranteed, any order, even within one topic | Explicitly not guaranteed, any order |
| **Retry/backoff** | Full-jitter exponential: 1s base, 2x, 30s cap, 6 attempts (~61s), then endpoint halts (manual resume) | Exponential backoff, automatic, over up to **3 days** (live mode); sandbox: 3 tries over a few hours; count not specified, only duration | Exponential backoff, **8 attempts over 4 hours**, then subscription auto-removed + email sent | **No automatic retry at all** — only manual redelivery (UI/API), within a 3-day window |
| **Signing** | HMAC-SHA256 over `"{timestamp}.{raw_body}"`, `Webhook-*` headers, 5-min tolerance, receiver dual-checks old+new secret during rotation | HMAC-SHA256 over `"{timestamp}.{raw_body}"` (same formula), `Stripe-Signature` header, 5-min default tolerance, **sender** signs with both secrets during a rotation window (up to 24h) | HMAC-SHA256 over raw body, `X-Shopify-Hmac-Sha256` header, no timestamp/tolerance documented, no dual-secret window documented (rotation take up to 1hr to propagate) | HMAC-SHA256 over raw payload, `X-Hub-Signature-256` header, no timestamp/tolerance documented, no secret-rotation mechanism documented |
| **Idempotency** | Caller-supplied `Idempotency-Key` on publish/replay; stable `event_id` across retries and replays; at-least-once is a stated non-goal to fix | Dedupe on event `id`; explicit "may receive the same event more than once" guidance | Dedupe on `X-Shopify-Webhook-Id`; separate `X-Shopify-Event-Id` shared across deliveries from one merchant action | Dedupe on `X-GitHub-Delivery`; note: a manual redelivery reuses the *same* delivery ID as the original |
| **Replay** | Re-inserts into the same per-endpoint queue; can delay live traffic on that endpoint, by design | Manual resend (dashboard: 15 days, CLI: 30 days); explicitly does **not** cancel the automatic retry track; no documented interaction with live-traffic queueing/priority | No documented resend/replay API; recovery is manual re-subscribe + backfill via the API | Manual redelivery only, 3-day lookback; no documented interaction with live-traffic priority |
| **Tenant fairness** | One rule: claim query orders by least-recently-served tenant, then oldest pending delivery — no cap | Not publicly documented | Not publicly documented | Not publicly documented |
| **Visibility** | Polling UI (2–5s); delivery detail screen shows full attempt timeline (sent, received, next retry) | Dashboard "Event deliveries" tab: per-event status, HTTP code, next-retry time | Dev Dashboard "Monitoring" (7-day aggregate) + "Logs" (per-delivery detail: response, timing, HMAC) | "Recent Deliveries" UI: request/response, timestamp, GUID, redeliver button — 3-day window only |

## Stripe

Stripe's own docs are unusually explicit about most of these mechanisms, in the single
[Receive Stripe events in your webhook endpoint](https://docs.stripe.com/webhooks) page (its
`#event-ordering`, `#automatic-retries`, `#manual-retries`, `#handle-duplicate-events`,
`#roll-endpoint-secrets`, `#preventing-replay-attacks`, and `#view-event-deliveries` sections).

- **Ordering**: not guaranteed, stated plainly: *"Stripe doesn't guarantee the delivery of events
  in the order that they're generated."* The doc gives a concrete example (a subscription's
  `customer.subscription.created` / `invoice.created` / `invoice.paid` / `charge.created` events
  can arrive out of order) and tells integrators to refetch missing state via the API rather than
  depend on order. This is the opposite of this project's strict-FIFO-per-endpoint guarantee.
  Stripe's choice makes sense at their fan-out scale across arbitrarily many downstream event
  types per action; this project's choice makes sense because R-required per-endpoint ordering
  was an explicit spec requirement here, not a nice-to-have.
- **Retry/backoff**: *"Stripe attempts to deliver events to your destination for up to three days
  with an exponential back off in live mode."* Sandbox mode retries 3 times over a few hours. Note
  Stripe frames this as a **duration budget**, not a fixed attempt count — unlike this project's
  fixed 6-attempt ceiling. Stripe also supports **manual** resend from the dashboard (15 days) or
  CLI (`stripe events resend`, 30 days), and is explicit that *"manually resending an event... 
  doesn't dismiss Stripe's automatic retry behavior"* — the two tracks run independently. This
  project's halt-and-require-explicit-resume model is a real divergence: Stripe never fully gives
  up on an event within its 3-day window and doesn't describe an equivalent of "endpoint halted,
  needs an operator" — reasonable for Stripe's scale (silently exhausting retries on payment
  events would be a business risk they'd rather absorb with a longer window than force human
  intervention early).
- **Signing**: HMAC-SHA256, and the exact signed-string construction —
  `signed_payload = timestamp + "." + payload` — is **the same formula this project uses**. The
  real divergence is secret rotation: Stripe's ["roll secret"](https://docs.stripe.com/webhooks#roll-endpoint-secrets)
  keeps the **old secret active on the sender side** for up to 24 hours and signs each outgoing
  request once per active secret (*"Stripe generates one signature per secret until expiration"*)
  — so Stripe pushes the complexity onto itself (multi-signing), whereas this project pushes it
  onto the receiver (dual-check old+new). Both solve the same rotation-without-downtime problem;
  the trade-off is who carries the complexity, and Stripe's version costs the receiver strictly
  less integration work.
- **Idempotency**: explicit guidance to log processed event IDs and skip repeats, with a caveat
  that duplicate *Event objects* (not just duplicate deliveries) can occur for the same underlying
  change, in which case dedupe on `data.object` id + `event.type` instead. Same
  at-least-once framing this project uses, though Stripe doesn't have an explicit
  `Idempotency-Key`-on-publish equivalent for webhook delivery (that header exists in Stripe's API
  for direct API calls, not for the webhook fan-out path itself).
- **Replay**: manual resend exists and is well-documented, but Stripe's docs say nothing about how
  a resend interacts with concurrent live delivery to the same endpoint — no priority lane is
  mentioned, but no explicit "it queues behind live traffic" statement either. This is a genuine
  gap in what's publicly knowable, not a case where Stripe is silent by omission of a feature —
  it's silent on the queueing mechanics specifically.
- **Tenant fairness**: not publicly documented. A web search surfaced only third-party blog
  commentary (e.g., Hookdeck) speculating about per-account rate limits and queueing patterns —
  useful for building a receiver, irrelevant to how Stripe's own sender infrastructure isolates
  merchants from each other, which Stripe does not describe anywhere in its docs.
- **Visibility**: the Workbench "Event deliveries" tab shows delivery status
  (Delivered/Pending/Failed), HTTP status code per attempt, and the time of the next pending
  retry — a real analog to this project's delivery-detail screen, though Stripe's docs don't
  describe it as showing a full multi-attempt timeline in one view the way this project's spec
  requires (attempt-by-attempt history with response bodies).

## Shopify

Primary sources: [About webhooks](https://shopify.dev/docs/apps/build/webhooks),
[Webhook delivery structure](https://shopify.dev/docs/apps/build/webhooks/delivery-structure),
[Verify webhook deliveries](https://shopify.dev/docs/apps/build/webhooks/verify-deliveries),
[Troubleshoot webhooks](https://shopify.dev/docs/apps/build/webhooks/troubleshooting-webhooks),
and the dev-changelog entry
[Updates to webhook retry mechanism](https://shopify.dev/changelog/updates-to-webhook-retry-mechanism)
(dated 2024-09-10) — the changelog is Shopify's own developer-changelog feed, treated here as
primary since it's first-party and dated, not a blog post.

- **Ordering**: not guaranteed, and unusually blunt about it: *"Shopify doesn't guarantee ordering
  within a topic, or across different topics for the same resource"* — their own example is a
  `products/update` webhook potentially arriving before `products/create` for the same product.
  Guidance is to use `X-Shopify-Triggered-At` or the payload's `updated_at` field to reconstruct
  sequence. Same divergence from this project as Stripe: no per-resource FIFO guarantee at all,
  compared to this project's strict per-endpoint FIFO.
- **Retry/backoff**: per the September 2024 changelog, *"Webhooks will now be retried a total of 8
  times over 4 hours using an exponential backoff schedule."* The
  [troubleshooting page](https://shopify.dev/docs/apps/build/webhooks/troubleshooting-webhooks)
  confirms this is current and adds the failure endgame: if delivery keeps failing, *"the
  subscription is removed"* and a warning email goes to the app's registered emergency developer
  address before that happens. This is structurally the closest of the three vendors to this
  project's design — a fixed attempt count, exponential backoff, and a terminal state requiring
  action — though Shopify deletes the subscription outright rather than this project's halted
  status that an operator can resume. Losing the subscription (vs. this project's retained,
  resumable `halted` endpoint) is a meaningfully harsher failure mode for the integrator: Shopify
  discards the registration, this project discards nothing and keeps every attempt's history.
- **Signing**: HMAC-SHA256 over the raw request body, base64-encoded in the
  `X-Shopify-Hmac-Sha256` header. No timestamp field and no documented tolerance window — Shopify
  relies on TLS + HMAC alone, with no replay-attack mitigation via timestamp the way this project
  (and Stripe) do. Secret rotation is thin by comparison: the docs only note that after rotating
  an app's client secret, *"it can take up to an hour for the HMAC digest to be generated using
  the new secret"* — a propagation delay, not an overlap window with dual-secret signing or a
  documented expectation that receivers check both secrets. This is a real gap relative to both
  this project's and Stripe's explicit rotation story.
- **Idempotency**: `X-Shopify-Webhook-Id` is *"a unique composite key per delivery. Use to
  identify and deduplicate."* Separately, `X-Shopify-Event-Id` is *"a unique ID shared across all
  deliveries produced by the same merchant action"* — this is a close structural match to this
  project's `event_id` (stable across retries/fan-out) vs. `delivery_id` (unique per attempt/per
  endpoint) split, just under different names.
- **Replay**: no vendor-documented resend/replay endpoint or dashboard action was found. Recovery
  from a dropped subscription is manual: re-subscribe and *"fetch data from the outage period and
  feed it into your webhook processing code"* via the regular API. This is a real capability gap
  relative to this project (and Stripe/GitHub), which both have first-class replay.
- **Tenant fairness**: not publicly documented.
- **Visibility**: the Dev Dashboard's
  [Monitoring and logs](https://shopify.dev/docs/apps/build/dev-dashboard/monitoring-and-logs)
  page provides a 7-day aggregate ("Monitoring": delivery counts and response time per topic) plus
  a per-delivery "Logs" view filterable by topic/status/shop, showing delivery attempt, response
  time, and the HMAC value for each entry. This is close to — though scoped to 7 days, shorter
  than this project's unbounded retention — a delivery-detail screen.

## GitHub

Primary sources:
[Handling webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/handling-webhook-deliveries),
[Validating webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries),
[Best practices for using webhooks](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks),
[Redelivering webhooks](https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/redelivering-webhooks),
[Viewing webhook deliveries](https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/viewing-webhook-deliveries),
and [Troubleshooting webhooks](https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/troubleshooting-webhooks).

- **Ordering**: not guaranteed — *"GitHub may deliver webhooks in a different order than the order
  in which the events took place"* — with the same "use payload timestamps" advice as Stripe and
  Shopify.
- **Retry/backoff**: this is where GitHub is the outlier of the three, and worth naming clearly:
  *"GitHub does not automatically redeliver failed deliveries."* There is **no automatic retry
  mechanism at all** — only manual redelivery, via the "Recent Deliveries" UI or API, and only for
  deliveries *"from the past 3 days."* Every other vendor here (and this project) auto-retries
  with backoff; GitHub puts 100% of the recovery burden on the integrator noticing a failure and
  clicking redeliver (or scripting it) within a 3-day window, after which the delivery is gone for
  good. This project's halt-after-6-attempts-then-require-an-operator model looks conservative by
  comparison — GitHub doesn't even attempt an automatic retry once, let alone six.
- **Signing**: HMAC-SHA256 over the raw payload bytes, hex-encoded (not base64) with a `sha256=`
  prefix, in the `X-Hub-Signature-256` header (`X-Hub-Signature` with SHA-1 is also sent for
  legacy compatibility — GitHub is the only one of the three still using SHA-1 anywhere in its
  main webhook auth path). No timestamp field, no documented tolerance window, and no documented
  secret-rotation mechanism at all — a real gap next to this project's explicit 5-minute tolerance
  and rotation-overlap story.
- **Idempotency**: `X-GitHub-Delivery` is the header to dedupe on, with a specific documented
  caveat: *"If you request a redelivery, the `X-GitHub-Delivery` header will be the same as in the
  original delivery."* That's actually consistent with (not a departure from) this project's
  model — a redelivery/replay carries the *same* durable identity as the thing it's replaying,
  same as this project's replayed deliveries reusing the original `event_id`.
- **Replay**: manual redelivery only (see retry above), 3-day lookback, no documented interaction
  with concurrent live traffic — same undocumented-queueing-mechanics gap as Stripe.
- **Tenant fairness**: not publicly documented.
- **Visibility**: "Recent Deliveries" shows *"the request headers and payload that GitHub sent,"*
  *"the response that GitHub received from your server,"* the send time, and a delivery GUID, with
  a redeliver action — functionally the closest of the three vendors to this project's
  delivery-detail requirement (request + response + timing in one view), but capped at a 3-day
  retention window versus this project's unbounded history.

## What this suggests

The signing scheme is the strongest validation: this project's `"{timestamp}.{raw_body}"` HMAC
construction is *exactly* Stripe's, and the 5-minute tolerance matches Stripe's documented default
— that's not a coincidence so much as convergent evolution on a well-understood pattern, which is
a good sign this piece is right. The ordering decision is this project's sharpest departure from
all three vendors — Stripe, Shopify, and GitHub each explicitly refuse to guarantee order and push
sequencing onto the receiver via timestamps, while this project guarantees strict per-endpoint
FIFO — but that's a deliberate response to a spec requirement none of these three vendors had to
satisfy, not a naive gap. The retry/halt model sits in reasonable company: closer to Shopify's
fixed-attempt-then-terminal-action shape than to Stripe's open-ended 3-day window or GitHub's
no-auto-retry-at-all stance, and this project's choice to retain a resumable `halted` state (rather
than Shopify's outright subscription deletion) is arguably the more integrator-friendly of the
two. The one real gap worth naming if this ever moved toward production: none of the four systems
compared here — including this one — publicly document how they prevent one tenant's traffic from
starving another's on shared delivery infrastructure, which means this project's single
least-recently-served-tenant sort order is comparatively *more* transparent than any of the three
vendors, but it's also the least battle-tested of the seven mechanisms, since there's no
production precedent to check it against.
