package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"webhooks-go/internal/api"
	"webhooks-go/internal/config"
	"webhooks-go/internal/crypto"
	"webhooks-go/internal/db"
	"webhooks-go/internal/secrets"
)

// Isolated Go-stack test database (go/README.md), separate from Node's
// webhooks_node_test on port 5532. Sequential by construction (no
// t.Parallel() calls) — every test truncates shared tables, same reason
// node/vitest.config.ts disables fileParallelism.
const testDatabaseURL = "postgres://webhooks:webhooks@localhost:5533/webhooks_go_test"

// 32 raw bytes — AES-256-GCM's exact key length. Test-only; production reads
// this from SECRET_ENCRYPTION_KEY via config.SecretEncryptionKey().
var testSecretKey = []byte("abcdefghijklmnopqrstuvwxyz012345")

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

func newTestServer(t *testing.T, pool *pgxpool.Pool) *httptest.Server {
	t.Helper()
	srv := api.NewServer(pool, testSecretKey, config.SecretRotationConfig{OverlapHours: 24})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func createTenant(t *testing.T, pool *pgxpool.Pool) (id, apiKey string) {
	t.Helper()
	apiKey = secrets.Generate("tenant")
	err := pool.QueryRow(context.Background(),
		`INSERT INTO tenants (name, api_key_hash) VALUES ('test-tenant', $1) RETURNING id`,
		crypto.HashAPIKey(apiKey),
	).Scan(&id)
	require.NoError(t, err)
	return id, apiKey
}

// createEndpoint bypasses the API (direct insert) for fixtures that need an
// endpoint to already exist before the behavior under test runs.
func createEndpoint(t *testing.T, pool *pgxpool.Pool, tenantID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO endpoints (tenant_id, url, event_types, signing_secret)
		 VALUES ($1, 'https://example.com/hook', ARRAY['order.created'], 'encrypted-placeholder')
		 RETURNING id`,
		tenantID,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// createDelivery inserts a delivery row directly (an event is required by
// the foreign key but its content doesn't matter for endpoint-route tests).
func createDelivery(t *testing.T, pool *pgxpool.Pool, tenantID, endpointID, state string) string {
	t.Helper()
	ctx := context.Background()

	var eventID string
	err := pool.QueryRow(ctx,
		`INSERT INTO events (tenant_id, idempotency_key, type, payload, status)
		 VALUES ($1, gen_random_uuid()::text, 'order.created', '{}', 'expanded')
		 RETURNING id`,
		tenantID,
	).Scan(&eventID)
	require.NoError(t, err)

	var deliveryID string
	err = pool.QueryRow(ctx,
		`INSERT INTO deliveries (event_id, endpoint_id, state) VALUES ($1, $2, $3) RETURNING id`,
		eventID, endpointID, state,
	).Scan(&deliveryID)
	require.NoError(t, err)
	return deliveryID
}

func doRequest(t *testing.T, method, url, apiKey string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequest(method, url, reader)
	require.NoError(t, err)
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
}
