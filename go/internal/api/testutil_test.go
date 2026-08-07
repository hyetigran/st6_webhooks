package api_test

import (
	"bytes"
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

var (
	setupPool      = testsupport.SetupPool
	createTenant   = testsupport.CreateTenant
	createEndpoint = testsupport.CreateEndpoint
)

func newTestServer(t *testing.T, pool *pgxpool.Pool) *httptest.Server {
	t.Helper()
	srv := api.NewServer(pool, testsupport.SecretEncryptionKey, config.SecretRotationConfig{OverlapHours: 24})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// createDelivery only needs the delivery id — endpoint-route tests
// (pause/resume disclosure) don't care about the backing event.
func createDelivery(t *testing.T, pool *pgxpool.Pool, tenantID, endpointID, state string) string {
	id, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{State: state})
	return id
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
