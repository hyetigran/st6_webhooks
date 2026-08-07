// Package testsupport holds Postgres-backed test fixtures shared across
// this project's Go packages' test suites (internal/api, internal/worker,
// ...). Only *_test.go files import this — never production code — so
// testify/testing never leak into a built binary.
package testsupport

import (
	"context"
	"net/http"
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

const testAdminDatabaseURL = "postgres://webhooks:webhooks@localhost:5533/postgres"
const testDatabaseName = "webhooks_go_test"

// ensureTestDatabase creates webhooks_go_test if it doesn't exist yet — so
// `make test` works on a fresh clone with no manual `createdb` step, the
// same as node/test/global-setup.ts's ensureTestDatabase does for the Node
// stack. Deliberately not shared with internal/scenariosupport's near-
// identical ensureDatabase: that package serves make chaos/make load, a
// separate concern from the plain `go test` path.
func ensureTestDatabase(ctx context.Context) error {
	admin, err := pgxpool.New(ctx, testAdminDatabaseURL)
	if err != nil {
		return err
	}
	defer admin.Close()

	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", testDatabaseName).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = admin.Exec(ctx, "CREATE DATABASE "+testDatabaseName)
	return err
}

// SecretEncryptionKey is the AES-256-GCM key shared by every test suite's
// fixtures and server construction, so an endpoint's encrypted-at-rest
// signing secret (however it was inserted) always decrypts correctly under
// whatever server or worker code the test exercises.
var SecretEncryptionKey = []byte("abcdefghijklmnopqrstuvwxyz012345")

var (
	migrateOnce sync.Once
	migrateErr  error
)

// SetupPool connects to the test database, migrates it (once per test
// binary), and truncates every table so each test starts from a clean
// slate. Every caller must run under `go test -p 1` (see ../../Makefile) —
// every package's tests share this one database, so cross-package
// parallelism would race one package's TRUNCATE against another's fixtures.
func SetupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	// migrateErr is package-level, not local — sync.Once.Do only ever runs
	// its closure on the very first call; every later call in this test
	// binary must still see whatever that first call's outcome was, not a
	// fresh (nil) local variable that would silently report success.
	migrateOnce.Do(func() {
		if migrateErr = ensureTestDatabase(ctx); migrateErr != nil {
			return
		}
		pool, err := db.NewPool(ctx, TestDatabaseURL)
		if err != nil {
			migrateErr = err
			return
		}
		defer pool.Close()
		migrateErr = db.Migrate(ctx, pool)
	})
	setupErr := migrateErr
	require.NoError(t, setupErr)

	pool, err := db.NewPool(ctx, TestDatabaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

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

// EventOptions customizes CreateEvent's fixture. Every field is optional;
// zero values fall back to a sensible default.
type EventOptions struct {
	Type      string
	Payload   string // raw JSON; defaults to {"hello":"world"}
	Status    string
	CreatedAt *time.Time
}

// CreateEvent inserts an event fixture directly (bypasses publish) — read-
// API tests need control over fields (created_at, in particular) publish
// doesn't expose. Mirrors node/test/fixtures.ts's createEvent.
func CreateEvent(t *testing.T, pool *pgxpool.Pool, tenantID string, opts EventOptions) string {
	t.Helper()

	eventType := opts.Type
	if eventType == "" {
		eventType = "order.created"
	}
	payload := opts.Payload
	if payload == "" {
		payload = `{"hello":"world"}`
	}
	status := opts.Status
	if status == "" {
		status = "expanded"
	}

	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO events (tenant_id, idempotency_key, type, payload, status, created_at)
		 VALUES ($1, $2, $3, $4, $5, COALESCE($6, now()))
		 RETURNING id`,
		tenantID, "event-fixture-"+uuid.NewString(), eventType, payload, status, opts.CreatedAt,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// AttemptOptions customizes CreateAttempt's fixture. Every field is
// optional; zero values fall back to a sensible default.
type AttemptOptions struct {
	AttemptNumber         int
	SentAt                *time.Time
	ResponseStatus        *int
	ResponseBodyTruncated *string
	DurationMs            *int
	ErrorClass            *string
}

// CreateAttempt inserts an attempt fixture directly for a given delivery.
// Mirrors node/test/fixtures.ts's createAttempt.
func CreateAttempt(t *testing.T, pool *pgxpool.Pool, deliveryID string, opts AttemptOptions) string {
	t.Helper()

	attemptNumber := opts.AttemptNumber
	if attemptNumber == 0 {
		attemptNumber = 1
	}
	sentAt := opts.SentAt
	if sentAt == nil {
		now := time.Now()
		sentAt = &now
	}

	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO attempts (delivery_id, attempt_number, sent_at, response_status, response_body_truncated, duration_ms, error_class)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		deliveryID, attemptNumber, sentAt, opts.ResponseStatus, opts.ResponseBodyTruncated, opts.DurationMs, opts.ErrorClass,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// ConformingReceiver is the reference fixture for REVIEW.md F-13 / PRD §6:
// receivers must dedupe on *successfully processed* event_id, not on
// event_id merely seen at attempt time — a receiver that marks an id "seen"
// before processing succeeds would silently no-op the replay of an event
// that never actually completed, defeating the reason to replay it.
// ProcessedEventIDs only ever gains an entry on the attempt where
// shouldSucceed says the receiver's business logic actually completed,
// never on mere receipt. Safe for concurrent requests (Go's http.Server
// handles each on its own goroutine, unlike Node's single-threaded
// equivalent).
type ConformingReceiver struct {
	mu                sync.Mutex
	processedEventIDs map[string]bool
	attemptsByEventID map[string]int
	shouldSucceed     func(eventID string, attemptNumber int) bool
}

func NewConformingReceiver(shouldSucceed func(eventID string, attemptNumber int) bool) *ConformingReceiver {
	return &ConformingReceiver{
		processedEventIDs: map[string]bool{},
		attemptsByEventID: map[string]int{},
		shouldSucceed:     shouldSucceed,
	}
}

func (r *ConformingReceiver) Handler(w http.ResponseWriter, req *http.Request) {
	eventID := req.Header.Get("Webhook-Event-Id")

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.processedEventIDs[eventID] {
		w.WriteHeader(http.StatusOK) // idempotent no-op — already successfully processed
		return
	}

	r.attemptsByEventID[eventID]++
	attemptNumber := r.attemptsByEventID[eventID]

	if r.shouldSucceed(eventID, attemptNumber) {
		r.processedEventIDs[eventID] = true
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (r *ConformingReceiver) HasProcessed(eventID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.processedEventIDs[eventID]
}

func (r *ConformingReceiver) AttemptCount(eventID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attemptsByEventID[eventID]
}
