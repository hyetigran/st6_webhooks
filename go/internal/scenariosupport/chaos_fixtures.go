package scenariosupport

import (
	"context"
	"encoding/base64"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"webhooks-go/internal/crypto"
)

const ChaosDatabaseName = "webhooks_go_chaos"

var ChaosDatabaseURL = "postgres://webhooks:webhooks@localhost:5533/" + ChaosDatabaseName

// SpawnChaosWorker starts the pre-built chaosworker binary with small
// timeouts/poll-interval by default — PRD §8's "time is injected": chaos
// scenarios need lease expiry and backoff to happen in seconds, not the
// production defaults' tens of seconds. envOverrides are applied on top of
// (and can override) these defaults.
func SpawnChaosWorker(binPath string, envOverrides map[string]string) (*ManagedProcess, error) {
	defaults := map[string]string{
		"DATABASE_URL":                 ChaosDatabaseURL,
		"SECRET_ENCRYPTION_KEY":        base64.StdEncoding.EncodeToString(ScenarioSecretEncryptionKey),
		"WORKER_IDLE_POLL_INTERVAL_MS": "50",
		"OUTBOUND_CONNECT_TIMEOUT_MS":  "1000",
		"OUTBOUND_TOTAL_TIMEOUT_MS":    "1000",
		"LEASE_MIN_DURATION_MS":        "1000",
		"BACKOFF_BASE_DELAY_MS":        "50",
		"BACKOFF_MAX_DELAY_MS":         "200",
	}
	for k, v := range envOverrides {
		defaults[k] = v
	}
	env := os.Environ()
	for k, v := range defaults {
		env = append(env, k+"="+v)
	}
	return StartProcess(binPath, nil, env)
}

func CreateEndpoint(ctx context.Context, pool *pgxpool.Pool, tenantID string, eventTypes []string, url, signingSecret string) (string, error) {
	if url == "" {
		url = "https://example.com/hook"
	}
	if signingSecret == "" {
		signingSecret = "whsec_chaos"
	}
	encrypted, err := crypto.EncryptSecret(signingSecret, ScenarioSecretEncryptionKey)
	if err != nil {
		return "", err
	}
	var id string
	err = pool.QueryRow(ctx,
		"INSERT INTO endpoints (tenant_id, url, event_types, signing_secret) VALUES ($1, $2, $3, $4) RETURNING id",
		tenantID, url, eventTypes, encrypted,
	).Scan(&id)
	return id, err
}

func CreatePendingDelivery(ctx context.Context, pool *pgxpool.Pool, tenantID, endpointID string) (id, eventID string, err error) {
	err = pool.QueryRow(ctx,
		`INSERT INTO events (tenant_id, idempotency_key, type, payload, status)
		 VALUES ($1, $2, 'order.created', '{"hello":"chaos"}', 'expanded')
		 RETURNING id`,
		tenantID, "chaos-fixture-"+uuid.NewString(),
	).Scan(&eventID)
	if err != nil {
		return "", "", err
	}
	err = pool.QueryRow(ctx,
		"INSERT INTO deliveries (event_id, endpoint_id, next_attempt_at) VALUES ($1, $2, now()) RETURNING id",
		eventID, endpointID,
	).Scan(&id)
	return id, eventID, err
}
