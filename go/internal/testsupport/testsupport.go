// Package testsupport holds Postgres-backed test fixtures shared across
// this project's Go packages' test suites (internal/api, internal/worker,
// ...). Only *_test.go files import this — never production code — so
// testify/testing never leak into a built binary.
package testsupport

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"webhooks-go/internal/crypto"
	"webhooks-go/internal/db"
	"webhooks-go/internal/secrets"
)

// TestDatabaseURL is the isolated Go-stack test database (go/README.md),
// separate from Node's webhooks_node_test on port 5532.
const TestDatabaseURL = "postgres://webhooks:webhooks@localhost:5533/webhooks_go_test"

var migrateOnce sync.Once

// SetupPool connects to the test database, migrates it (once per test
// binary), and truncates every table so each test starts from a clean
// slate. Every caller must run under `go test -p 1` (see ../../Makefile) —
// every package's tests share this one database, so cross-package
// parallelism would race one package's TRUNCATE against another's fixtures.
func SetupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pool, err := db.NewPool(ctx, TestDatabaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	migrateOnce.Do(func() {
		require.NoError(t, db.Migrate(ctx, pool))
	})

	_, err = pool.Exec(ctx, "TRUNCATE tenants, endpoints, events, deliveries, attempts, replays RESTART IDENTITY CASCADE")
	require.NoError(t, err)

	return pool
}

// CreateTenant inserts a tenant fixture, returning its id and plaintext API
// key.
func CreateTenant(t *testing.T, pool *pgxpool.Pool) (id, apiKey string) {
	t.Helper()
	apiKey = secrets.Generate("tenant")
	err := pool.QueryRow(context.Background(),
		`INSERT INTO tenants (name, api_key_hash) VALUES ('test-tenant', $1) RETURNING id`,
		crypto.HashAPIKey(apiKey),
	).Scan(&id)
	require.NoError(t, err)
	return id, apiKey
}

// CreateEndpoint inserts an endpoint fixture subscribed to eventTypes.
// status defaults to "active" when empty.
func CreateEndpoint(t *testing.T, pool *pgxpool.Pool, tenantID string, eventTypes []string, status string) string {
	t.Helper()
	if status == "" {
		status = "active"
	}
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO endpoints (tenant_id, url, event_types, signing_secret, status)
		 VALUES ($1, 'https://example.com/hook', $2, 'encrypted-placeholder', $3)
		 RETURNING id`,
		tenantID, eventTypes, status,
	).Scan(&id)
	require.NoError(t, err)
	return id
}
