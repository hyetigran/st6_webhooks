# Progress

What works, what doesn't exist yet, and known issues. For live ticket-level status, the
wayfinder map (GitLab issue #1) is authoritative — this file is a snapshot for fast orientation,
not a replacement for it.

## What works

- **Node — endpoint management API** (`node/`): schema migrated, Bearer auth, R-2 URL
  validation (IPv4/IPv6, literal + DNS-resolved private/loopback/link-local/CGNAT rejection),
  all six endpoint routes (register/list/detail/pause/resume/rotate-secret). Verified live
  against a real Postgres instance — see ticket #16's resolution comment for the full test
  transcript.

## What's built but not yet exercised end-to-end

Nothing currently — everything built so far has been verified live.

## What doesn't exist yet

- **Node**: publish/expansion (#17), delivery worker — the core ordering/lease/fairness/signing/
  backoff/halt mechanics (#18), replay (#19), visibility/read API (#20), test suite + deployment
  docs (#21).
- **Go**: the entire stack (#22-27), mirroring Node ticket-for-ticket.
- **Frontend**: entire SPA (#28-30) — buildable now against the fixed REST contract, doesn't
  need to wait on real backends.
- **`README.md`**: not started for either stack (part of #21/#27).
- **Primary-build designation**: can't be decided until both stacks have a working `make load`
  test to measure (see `DECISIONS.md`, "Submission").

## Known issues / gotchas for future sessions

- **Port 5433 was already taken** by an unrelated local project when Node's Postgres was set up
  — Node uses **5532** instead. Check port availability with `lsof` before assuming a "standard"
  port is free; this will likely recur when Go's docker-compose is created.
- **Express 4 async error handling**: routes must use `src/lib/asyncHandler.ts` or a thrown
  error in an async handler hangs the request instead of producing a 500. Easy to forget when
  adding new routes in later Node tickets.
- **npm audit flags 5 vulnerabilities** in `node/`, all transitive dev-dependencies of `vitest`'s
  bundled `esbuild` dev server (not production code, not shipped). Not worth fixing unless it
  starts blocking something; noted here so it isn't rediscovered and treated as urgent.
