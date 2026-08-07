package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"webhooks-go/internal/api"
	"webhooks-go/internal/config"
	"webhooks-go/internal/testsupport"
)

// 32 raw bytes — AES-256-GCM's exact key length. Test-only; production reads
// this from SECRET_ENCRYPTION_KEY via config.SecretEncryptionKey().
var testSecretKey = []byte("abcdefghijklmnopqrstuvwxyz012345")

var (
	setupPool      = testsupport.SetupPool
	createTenant   = testsupport.CreateTenant
	createEndpoint = testsupport.CreateEndpoint
)

func newTestServer(t *testing.T, pool *pgxpool.Pool) *httptest.Server {
	t.Helper()
	srv := api.NewServer(pool, testSecretKey, config.SecretRotationConfig{OverlapHours: 24})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
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

// newJSONRequest builds a request with a JSON body and Content-Type set,
// leaving Authorization/Idempotency-Key/etc for the caller — for tests that
// need headers beyond what doRequest's fixed apiKey param covers.
func newJSONRequest(method, url string, body any) (*http.Request, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
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
