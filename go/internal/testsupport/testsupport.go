// Package testsupport holds Postgres-backed test fixtures shared across
// this project's Go packages' test suites (internal/api, internal/worker,
// ...). Only *_test.go files import this — never production code — so
// testify/testing never leak into a built binary.
package testsupport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"webhooks-go/internal/crypto"
	"webhooks-go/internal/db"
	"webhooks-go/internal/secrets"
)

// TestDatabaseURL is the isolated Go-stack test database (go/README.md),
// separate from Node's webhooks_node_test on port 5532.
const TestDatabaseURL = "postgres://webhooks:webhooks@localhost:5533/webhooks_go_test"

// SecretEncryptionKey is the AES-256-GCM key shared by every test suite's
// fixtures and server construction, so an endpoint's encrypted-at-rest
// signing secret (however it was inserted) always decrypts correctly under
// whatever server or worker code the test exercises.
var SecretEncryptionKey = []byte("abcdefghijklmnopqrstuvwxyz012345")

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

// EndpointOptions customizes CreateEndpoint's fixture. Every field is
// optional; zero values fall back to a sensible default.
type EndpointOptions struct {
	Status                   string
	URL                      string
	SigningSecret            string
	SecondarySecret          string
	SecondarySecretExpiresAt *time.Time
}

// CreateEndpoint inserts an endpoint fixture subscribed to eventTypes.
func CreateEndpoint(t *testing.T, pool *pgxpool.Pool, tenantID string, eventTypes []string, opts EndpointOptions) string {
	t.Helper()

	status := opts.Status
	if status == "" {
		status = "active"
	}
	url := opts.URL
	if url == "" {
		url = "https://example.com/hook"
	}
	signingSecret := opts.SigningSecret
	if signingSecret == "" {
		signingSecret = "whsec_test"
	}
	encryptedSigning, err := crypto.EncryptSecret(signingSecret, SecretEncryptionKey)
	require.NoError(t, err)

	var encryptedSecondary *string
	if opts.SecondarySecret != "" {
		enc, err := crypto.EncryptSecret(opts.SecondarySecret, SecretEncryptionKey)
		require.NoError(t, err)
		encryptedSecondary = &enc
	}

	var id string
	err = pool.QueryRow(context.Background(),
		`INSERT INTO endpoints (tenant_id, url, event_types, status, signing_secret, secondary_secret, secondary_secret_expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		tenantID, url, eventTypes, status, encryptedSigning, encryptedSecondary, opts.SecondarySecretExpiresAt,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// DeliveryOptions customizes CreateDelivery's fixture. Every field is
// optional; zero values fall back to a sensible default.
type DeliveryOptions struct {
	EventType     string
	Payload       string // raw JSON; defaults to {"hello":"world"}
	NextAttemptAt *time.Time
	CreatedAt     *time.Time
	State         string
}

// CreateDelivery bypasses publish/expansion to seed a delivery (and its
// backing event) directly — delivery-worker tests exercise the claim/send
// path, not expansion, so they don't need a real publish flow. Mirrors
// node/test/fixtures.ts's createDelivery, including using Postgres's own
// now() for next_attempt_at/created_at defaults rather than a Go-side
// time.Now() — claim queries compare these columns against Postgres's own
// now(), and clock skew between this process and the Docker Postgres
// container would otherwise make "immediately claimable" intermittently
// wrong.
func CreateDelivery(t *testing.T, pool *pgxpool.Pool, tenantID, endpointID string, opts DeliveryOptions) (id, eventID string) {
	t.Helper()
	ctx := context.Background()

	eventType := opts.EventType
	if eventType == "" {
		eventType = "order.created"
	}
	payload := opts.Payload
	if payload == "" {
		payload = `{"hello":"world"}`
	}
	state := opts.State
	if state == "" {
		state = "pending"
	}

	err := pool.QueryRow(ctx,
		`INSERT INTO events (tenant_id, idempotency_key, type, payload, status)
		 VALUES ($1, $2, $3, $4, 'expanded')
		 RETURNING id`,
		tenantID, "delivery-fixture-"+uuid.NewString(), eventType, payload,
	).Scan(&eventID)
	require.NoError(t, err)

	err = pool.QueryRow(ctx,
		`INSERT INTO deliveries (event_id, endpoint_id, state, next_attempt_at, created_at)
		 VALUES ($1, $2, $3, COALESCE($4, now()), COALESCE($5, now()))
		 RETURNING id`,
		eventID, endpointID, state, opts.NextAttemptAt, opts.CreatedAt,
	).Scan(&id)
	require.NoError(t, err)
	return id, eventID
}
