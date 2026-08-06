# Replay expands asynchronously, mirroring publish exactly

The original replay design ran the window `SELECT` and the bulk delivery `INSERT` synchronously
inside the API request handler — the same O(request-size) blocking problem async expansion
(ADR-004) exists to prevent on publish, just for replay instead. Worse: with no transaction
boundary shown around the `replays` insert and the delivery inserts, a crash between the two
leaves the idempotency-key check (`ON CONFLICT DO NOTHING`) silently swallowing a retried
request and returning `202` with **zero** deliveries created — an empty replay that looks
successful. Separately, a replay window with no filter on delivery state could pull in
still-`pending` deliveries and duplicate them, even though the original will still be attempted
on its own schedule. Found during an adversarial review (`REVIEW.md` F-8).

**Decision:** replay gets the identical `status` (`pending_expansion` / `expanded`) and
async-expansion treatment events already have. `POST /endpoints/{id}/replays` does one thing
synchronously — insert the `replays` row — and returns `202` immediately; the durable ack *is*
that one insert, exactly mirroring publish. The same shared worker pool later expands a
pending replay: selects matching original deliveries in the window, creates the new delivery
rows, flips the replay to `expanded`, all in one atomic transaction. This fixes both the
O(window) latency problem and the crash-unsafe idempotency problem at once, for the same
reason ADR-004 fixed them for publish. Additionally: the window query excludes deliveries that
are still `pending` or `in_flight` — a not-yet-resolved delivery will be attempted on its own
schedule regardless, so replaying it too is pure duplication with no recovery benefit.

## Considered options

- **Keep replay synchronous, just wrap it in a transaction** — considered, rejected: fixes the
  crash-safety half but not the O(window) latency half, and introduces a second async-expansion
  pattern to maintain alongside events' instead of reusing the one that already exists.
- **Include non-terminal deliveries in the replay window** — considered, rejected: duplicates a
  delivery that was never actually a failure to recover from, since it hasn't been attempted
  yet. Simpler filter, worse behavior.

## Consequences

Replay now has the same two-phase shape as publish (fast synchronous ack, async worker-driven
expansion) — one mental model for both instead of two. `GET /endpoints/{id}/replays/{id}` (or
equivalent) would need a status field if a caller wants to poll replay progress, matching R-9's
treatment of `events.status` for the same reason (don't let "not expanded yet" read as
"nothing happened"). Not yet added to the REST contract; worth doing when replay's read-side
API is actually built.
