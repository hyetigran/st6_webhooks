# Webhook delivery service — Go

Partial build in progress; see the map issue and `../DECISIONS.md` for the architecture. This
README covers what's implemented so far (schema + endpoint management API). It'll be expanded
into a full clone-to-run guide by the "[Go] Test suite & deployment" ticket.

## Local dev

```
cp .env.example .env          # generate a real SECRET_ENCRYPTION_KEY with `openssl rand -base64 32`
docker compose up -d postgres
go run ./cmd/migrate
go run ./cmd/seed              # prints a demo tenant's API key
go run ./cmd/api                # listens on :8090
go run ./cmd/worker             # shared worker pool (expansion so far)
```

Tests need their own database (`webhooks_go_test`) — create it once, then run `make test` (not
bare `go test ./...`; see the Makefile comment for why `-p 1` matters here).

## Stack

- Go 1.26, stdlib `net/http` (1.22+ method + `{wildcard}` `ServeMux` routing, no framework).
- `pgx`/`pgxpool` — no ORM, plain SQL, mirroring the Node stack's use of `pg`.
- Hand-written `.sql` migrations under `internal/db/migrations/`, embedded into the binary via
  `go:embed` and applied by `cmd/migrate`.
- Local Postgres port **5533** (Node uses 5532 — kept separate, isolated instance per stack).
  API port **8090**.
