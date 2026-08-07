# Webhook delivery service — Go

A reliable webhook delivery system: register endpoints, publish events, get durable
at-least-once delivery with strict per-endpoint ordering, retries with backoff, replay, and a
full read API for answering "what happened to this event" without reading logs.

This is one of two architecturally-identical implementations built for `CASE_STUDY.md`'s
Project 1 — see the repo root's `DECISIONS.md` (why things are built this way) and
`ARCHITECTURE.md` (component/data-model reference) for the full design. This file is just
"clone it, run it, understand it" — everything else lives at the root.

## Prerequisites

- Go 1.26+
- Docker (for Postgres, and optionally for running the full stack)

## Quickstart

```sh
cd go
docker compose up -d postgres
cp .env.example .env          # generate a real SECRET_ENCRYPTION_KEY with `openssl rand -base64 32`
go run ./cmd/migrate
go run ./cmd/seed              # prints a demo tenant's API key — save it
go run ./cmd/api                # API on :8090
go run ./cmd/worker              # in a second terminal — shared worker pool
```

`GET /healthz` is unauthenticated. Every other route needs `Authorization: Bearer <api_key>`
from the seed step above.

## Try it

Using the API key from `go run ./cmd/seed`:

```sh
API_KEY=tenant_xxxxx  # from the seed step

# Register an endpoint. The URL must resolve to a real, non-private address —
# registration performs the same SSRF-safe DNS check delivery does (R-2/ADR-0006).
curl -s -X POST http://localhost:8090/endpoints \
  -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
  -d '{"url":"https://httpbin.org/post","event_types":["order.created"]}'
# => {"id":"...", "signing_secret":"whsec_...", ...}  — the secret is shown once (R-3)

# Publish an event. Idempotency-Key is a header, not a body field (R-6).
curl -s -X POST http://localhost:8090/events \
  -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
  -H "Idempotency-Key: order-123" \
  -d '{"type":"order.created","payload":{"orderId":"abc123"}}'
# => {"id":"...", "status":"pending_expansion"} (202) — the worker expands and delivers async

# Check what happened (poll — this is a UI-visibility API, not a webhook back to you):
curl -s http://localhost:8090/deliveries/<delivery_id> -H "Authorization: Bearer $API_KEY"
```

## Verifying a received webhook (receiver contract)

Every delivery carries five headers, no `X-` prefix (PRD §6):

| Header | Meaning |
| --- | --- |
| `Webhook-Id` | Unique per delivery attempt sequence |
| `Webhook-Event-Id` | Stable across retries and replays — dedupe on this |
| `Webhook-Attempt` | 1-indexed attempt number |
| `Webhook-Timestamp` | Unix seconds |
| `Webhook-Signature` | One HMAC-SHA256 hex digest per still-active secret, comma-joined |

```go
func verify(rawBody []byte, timestamp, signatureHeader, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + string(rawBody)))
	expected := hex.EncodeToString(mac.Sum(nil))
	// ADR-0003: during a rotation overlap window, the sender signs with every
	// still-active secret — check against all of them, not just the current one.
	for _, sig := range strings.Split(signatureHeader, ",") {
		if hmac.Equal([]byte(sig), []byte(expected)) {
			return true
		}
	}
	return false
}
```

Reject requests where `|now - timestamp| > 300s` (replay protection). Dedupe on
`Webhook-Event-Id` — **only after your handler has successfully processed it**, not merely
received it (PRD §6: deduping at receipt time would silently no-op the replay of an event that
never actually completed).

## Running the test suite

```sh
make test          # full Go test suite — unit + integration against a real Postgres
make typecheck      # go vet
```

Tests share one Postgres database (`webhooks_go_test`) via `TRUNCATE`-based fixtures — always
run via `make test`, not bare `go test ./...`, since cross-package parallelism races those
fixtures (see the Makefile comment).

For PRD.md §8's full acceptance criteria (chaos, property, and load tests — these spawn real
child processes and take longer):

```sh
make properties     # seeded, randomized invariants — logs its seed for reproduction
make chaos          # real process kill/SIGSTOP/SIGCONT against a real worker pool, ~10s
make load            # real HTTP latency against a real api/worker pool, ~10s
make verify          # everything above — writes evidence to ../evidence/go/{chaos,load}/
```

`make chaos`/`make load` each manage their own dedicated database
(`webhooks_go_chaos`/`webhooks_go_load`), separate from your dev data and the `go test` DB —
safe to run alongside `go run ./cmd/api`. Both rebuild `bin/{api,chaosworker,expansionholder}`
fresh first (`bin/` is gitignored) and exec those binaries directly, never `go run` — a chaos
scenario that `SIGKILL`s a `go run` wrapper only kills the wrapper, not the real process it
spawned, so anything needing real signal delivery needs a pre-built binary.

## Running the full stack in Docker

```sh
docker compose up -d --build
```

Brings up Postgres, the API, and the worker, each running `./migrate` on startup (serialized via
a Postgres advisory lock, since both services race it concurrently — see
`internal/db/migrate.go`). Same `Authorization`/`Idempotency-Key` contract as above, on `:8090`.
Seed a tenant from inside the container: `docker compose exec api ./seed <name>`.

## Project structure

```
cmd/
  api/            HTTP server entrypoint
  worker/          the poll-loop entrypoint (runs expansion/delivery/replay-expansion cycles
                    across a WORKER_POOL_SIZE goroutine pool, one process)
  migrate/, seed/  one-shot admin commands
internal/
  api/            endpoints, events (publish + search), deliveries, replays — the REST surface
  worker/          expansion, delivery, replayExpansion — the shared worker pool's three cycles
  signing/, validation/, pagination/, secrets/, crypto/, config/, db/
  testsupport/     fixtures for `go test` (real Postgres, no mocks)
  properties/      seeded PRNG for `make properties`
  scenariosupport/ shared chaos+load infra (process spawn/signal, evidence writing, fixtures)
chaos/            make chaos scenario programs, one per PRD §8 chaos row
load/             make load scenario programs, one per PRD §8 load row
```

## Known gotchas

- **Postgres runs on host port `5533`, not the Postgres default `5432`** — Node's stack uses
  `5532` on the same machine; kept separate, isolated instances per stack. Check
  `docker-compose.yml` if a port conflict comes up.
- **`SECRET_ENCRYPTION_KEY` is required, no fallback.** Must be a base64-encoded 32-byte key
  (`openssl rand -base64 32`). The app panics on startup if it's missing or the wrong length.
  `.env.example` ships a working local-dev key — generate your own for anything beyond that.
- **Endpoint URLs must resolve to a real, non-private address**, at both registration and
  delivery time (R-2/R-16, ADR-0006) — `http://localhost/...` or any RFC1918 address will be
  rejected. This is a deliberate SSRF defense, not a bug; use a real reachable URL (a tool like
  `httpbin.org/post`, or your own publicly-reachable dev tunnel) to try delivery locally.
- **Never wrap a chaos/load scenario's spawned process in `go run`.** `go run ./cmd/X` execs its
  compiled binary as a *child* of the `go run` process; `SIGKILL`/`SIGSTOP` sent to that wrapper
  never reaches the real process. `internal/scenariosupport.StartProcess` always execs a
  pre-built `bin/` binary directly — see its doc comment.
