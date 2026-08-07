package worker_test

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

// Same isolated Go-stack test database internal/api's tests use — the
// worker package needs the identical schema, just exercised at the pool
// level instead of through HTTP.
const testDatabaseURL = "postgres://webhooks:webhooks@localhost:5533/webhooks_go_test"

var migrateOnce sync.Once

func setupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pool, err := db.NewPool(ctx, testDatabaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	migrateOnce.Do(func() {
		require.NoError(t, db.Migrate(ctx, pool))
	})

	_, err = pool.Exec(ctx, "TRUNCATE tenants, endpoints, events, deliveries, attempts, replays RESTART IDENTITY CASCADE")
	require.NoError(t, err)

	return pool
}

func createTenant(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO tenants (name, api_key_hash) VALUES ('test-tenant', $1) RETURNING id`,
		crypto.HashAPIKey(secrets.Generate("tenant")),
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func createEndpoint(t *testing.T, pool *pgxpool.Pool, tenantID string, eventTypes []string, status string) string {
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

func publishEvent(t *testing.T, pool *pgxpool.Pool, tenantID, eventType, idempotencyKey string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO events (tenant_id, idempotency_key, type, payload) VALUES ($1, $2, $3, '{}') RETURNING id`,
		tenantID, idempotencyKey, eventType,
	).Scan(&id)
	require.NoError(t, err)
	return id
}
