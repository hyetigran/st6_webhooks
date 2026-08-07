package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/testsupport"
)

var validReplayBody = map[string]any{
	"range_start": "2026-01-01T00:00:00.000Z",
	"range_end":   "2026-01-02T00:00:00.000Z",
}

func TestReplayAcceptsRequestAndReturnsPendingExpansion(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})

	req, err := newJSONRequest(http.MethodPost, ts.URL+"/endpoints/"+endpointID+"/replays", validReplayBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Idempotency-Key", "replay-key-1")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	var body struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	decodeJSON(t, resp, &body)
	require.NotEmpty(t, body.ID)
	require.Equal(t, "pending_expansion", body.Status)
}

func TestReplayIsIdempotentOnRepeatedKey(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})

	postReplay := func() string {
		req, err := newJSONRequest(http.MethodPost, ts.URL+"/endpoints/"+endpointID+"/replays", validReplayBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Idempotency-Key", "repeat-replay-key")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusAccepted, resp.StatusCode)

		var body struct {
			ID string `json:"id"`
		}
		decodeJSON(t, resp, &body)
		return body.ID
	}

	first := postReplay()
	second := postReplay()
	require.Equal(t, first, second)
}

func TestReplayRejectsMissingIdempotencyKey(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})

	resp := doRequest(t, http.MethodPost, ts.URL+"/endpoints/"+endpointID+"/replays", apiKey, validReplayBody)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestReplayRejectsRangeEndBeforeRangeStart(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{})

	req, err := newJSONRequest(http.MethodPost, ts.URL+"/endpoints/"+endpointID+"/replays", map[string]any{
		"range_start": "2026-01-02T00:00:00.000Z",
		"range_end":   "2026-01-01T00:00:00.000Z",
	})
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Idempotency-Key", "reversed-range")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestReplayReturnsNotFoundForMissingEndpoint(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := testsupport.CreateTenant(t, pool)

	req, err := newJSONRequest(http.MethodPost, ts.URL+"/endpoints/00000000-0000-0000-0000-000000000000/replays", validReplayBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Idempotency-Key", "missing-endpoint")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestReplayReturnsNotFoundForCrossTenantEndpoint(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ts := newTestServer(t, pool)
	otherTenantID, _ := testsupport.CreateTenant(t, pool)
	foreignEndpointID := testsupport.CreateEndpoint(t, pool, otherTenantID, []string{"order.created"}, testsupport.EndpointOptions{})
	_, apiKey := testsupport.CreateTenant(t, pool)

	req, err := newJSONRequest(http.MethodPost, ts.URL+"/endpoints/"+foreignEndpointID+"/replays", validReplayBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Idempotency-Key", "cross-tenant")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
