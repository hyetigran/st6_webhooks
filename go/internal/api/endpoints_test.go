package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterEndpoint(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := createTenant(t, pool)

	resp := doRequest(t, http.MethodPost, ts.URL+"/endpoints", apiKey, map[string]any{
		"url":         "https://example.com/hook",
		"event_types": []string{"order.created"},
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var body struct {
		ID            string   `json:"id"`
		URL           string   `json:"url"`
		EventTypes    []string `json:"event_types"`
		Status        string   `json:"status"`
		CreatedAt     string   `json:"created_at"`
		SigningSecret string   `json:"signing_secret"`
	}
	decodeJSON(t, resp, &body)

	require.NotEmpty(t, body.ID)
	require.Equal(t, "https://example.com/hook", body.URL)
	require.Equal(t, []string{"order.created"}, body.EventTypes)
	require.Equal(t, "active", body.Status)
	require.NotEmpty(t, body.CreatedAt)
	require.Regexp(t, `^whsec_[0-9a-f]{48}$`, body.SigningSecret)

	// The signing secret stored in the DB must be the encrypted form, not
	// the plaintext value returned to the caller (R-3 / REVIEW.md F-16).
	var stored string
	err := pool.QueryRow(context.Background(), "SELECT signing_secret FROM endpoints WHERE id = $1", body.ID).Scan(&stored)
	require.NoError(t, err)
	require.NotEqual(t, body.SigningSecret, stored)
}

func TestRegisterEndpointRejectsInvalidBody(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := createTenant(t, pool)

	resp := doRequest(t, http.MethodPost, ts.URL+"/endpoints", apiKey, map[string]any{
		"event_types": []string{"order.created"},
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// R-2: registration rejects URLs that resolve to private, loopback, or
// link-local ranges (SSRF defense).
func TestRegisterEndpointRejectsLoopbackURL(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := createTenant(t, pool)

	resp := doRequest(t, http.MethodPost, ts.URL+"/endpoints", apiKey, map[string]any{
		"url":         "http://127.0.0.1:9999/hook",
		"event_types": []string{"order.created"},
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeJSON(t, resp, &body)
	require.Equal(t, "url_not_allowed", body.Error.Code)
}

func TestRegisterEndpointRequiresAuth(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)

	resp := doRequest(t, http.MethodPost, ts.URL+"/endpoints", "", map[string]any{
		"url":         "https://example.com/hook",
		"event_types": []string{"order.created"},
	})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp = doRequest(t, http.MethodPost, ts.URL+"/endpoints", "not-a-real-key", map[string]any{
		"url":         "https://example.com/hook",
		"event_types": []string{"order.created"},
	})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestGetEndpointIncludesHealthFields(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := createTenant(t, pool)
	endpointID := createEndpoint(t, pool, tenantID)

	resp := doRequest(t, http.MethodGet, ts.URL+"/endpoints/"+endpointID, apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		QueueDepth        int64    `json:"queue_depth"`
		OldestPendingAt   *string  `json:"oldest_pending_at"`
		RecentSuccessRate *float64 `json:"recent_success_rate"`
	}
	decodeJSON(t, resp, &body)
	require.Equal(t, int64(0), body.QueueDepth)
	require.Nil(t, body.OldestPendingAt)
	require.Nil(t, body.RecentSuccessRate)
}

func TestGetEndpointNotFound(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := createTenant(t, pool)

	resp := doRequest(t, http.MethodGet, ts.URL+"/endpoints/00000000-0000-0000-0000-000000000000", apiKey, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// Tenant isolation: tenant B must never see tenant A's endpoint, not even as
// a 403 that would confirm the id exists.
func TestGetEndpointIsScopedToTenant(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	tenantA, _ := createTenant(t, pool)
	_, apiKeyB := createTenant(t, pool)
	endpointID := createEndpoint(t, pool, tenantA)

	resp := doRequest(t, http.MethodGet, ts.URL+"/endpoints/"+endpointID, apiKeyB, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestListEndpointsPaginates(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := createTenant(t, pool)
	for i := 0; i < 3; i++ {
		createEndpoint(t, pool, tenantID)
	}

	resp := doRequest(t, http.MethodGet, ts.URL+"/endpoints?limit=2", apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var page1 struct {
		Endpoints  []struct{ ID string } `json:"endpoints"`
		NextCursor *string               `json:"next_cursor"`
	}
	decodeJSON(t, resp, &page1)
	require.Len(t, page1.Endpoints, 2)
	require.NotNil(t, page1.NextCursor)

	resp = doRequest(t, http.MethodGet, fmt.Sprintf("%s/endpoints?limit=2&before=%s", ts.URL, *page1.NextCursor), apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var page2 struct {
		Endpoints  []struct{ ID string } `json:"endpoints"`
		NextCursor *string               `json:"next_cursor"`
	}
	decodeJSON(t, resp, &page2)
	require.Len(t, page2.Endpoints, 1)
	require.Nil(t, page2.NextCursor)

	seen := map[string]bool{}
	for _, e := range append(page1.Endpoints, page2.Endpoints...) {
		require.False(t, seen[e.ID], "endpoint %s returned on more than one page", e.ID)
		seen[e.ID] = true
	}
	require.Len(t, seen, 3)
}

func TestPauseAndResumeEndpoint(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := createTenant(t, pool)
	endpointID := createEndpoint(t, pool, tenantID)

	resp := doRequest(t, http.MethodPost, ts.URL+"/endpoints/"+endpointID+"/pause", apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var paused struct{ Status string }
	decodeJSON(t, resp, &paused)
	require.Equal(t, "paused", paused.Status)

	resp = doRequest(t, http.MethodPost, ts.URL+"/endpoints/"+endpointID+"/resume", apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var resumed struct {
		Status                   string   `json:"status"`
		PendingDeliveryCount     int64    `json:"pending_delivery_count"`
		SkippedFailedDeliveryIDs []string `json:"skipped_failed_delivery_ids"`
	}
	decodeJSON(t, resp, &resumed)
	require.Equal(t, "active", resumed.Status)
	require.Equal(t, int64(0), resumed.PendingDeliveryCount)
	require.Empty(t, resumed.SkippedFailedDeliveryIDs)
}

// R-14 / REVIEW.md F-12: resume must disclose which failed deliveries it's
// leaving behind — a 'failed' delivery is terminal, resume never retries it.
func TestResumeDisclosesSkippedFailedDeliveries(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	tenantID, apiKey := createTenant(t, pool)
	endpointID := createEndpoint(t, pool, tenantID)

	failedID := createDelivery(t, pool, tenantID, endpointID, "failed")
	// A pending delivery newer than the failed one — the failed delivery is
	// older than the oldest still-pending delivery, so resume leaves it
	// behind rather than silently reconsidering it.
	createDelivery(t, pool, tenantID, endpointID, "pending")

	resp := doRequest(t, http.MethodPost, ts.URL+"/endpoints/"+endpointID+"/pause", apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = doRequest(t, http.MethodPost, ts.URL+"/endpoints/"+endpointID+"/resume", apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var resumed struct {
		PendingDeliveryCount     int64    `json:"pending_delivery_count"`
		SkippedFailedDeliveryIDs []string `json:"skipped_failed_delivery_ids"`
	}
	decodeJSON(t, resp, &resumed)
	require.Equal(t, int64(1), resumed.PendingDeliveryCount)
	require.Equal(t, []string{failedID}, resumed.SkippedFailedDeliveryIDs)
}

func TestRotateSecret(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := createTenant(t, pool)

	resp := doRequest(t, http.MethodPost, ts.URL+"/endpoints", apiKey, map[string]any{
		"url":         "https://example.com/hook",
		"event_types": []string{"order.created"},
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var registered struct {
		ID            string `json:"id"`
		SigningSecret string `json:"signing_secret"`
	}
	decodeJSON(t, resp, &registered)

	resp = doRequest(t, http.MethodPost, ts.URL+"/endpoints/"+registered.ID+"/secret/rotate", apiKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var rotated struct {
		SigningSecret    string `json:"signing_secret"`
		OverlapExpiresAt string `json:"overlap_expires_at"`
	}
	decodeJSON(t, resp, &rotated)
	require.NotEmpty(t, rotated.SigningSecret)
	require.NotEqual(t, registered.SigningSecret, rotated.SigningSecret)
	require.NotEmpty(t, rotated.OverlapExpiresAt)

	// docs/adr/0003: the old secret must move to secondary_secret (still
	// signed with during the overlap window), not be discarded.
	var secondarySecretEncrypted *string
	err := pool.QueryRow(context.Background(),
		"SELECT secondary_secret FROM endpoints WHERE id = $1", registered.ID,
	).Scan(&secondarySecretEncrypted)
	require.NoError(t, err)
	require.NotNil(t, secondarySecretEncrypted)
}

func TestRotateSecretNotFound(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := createTenant(t, pool)

	resp := doRequest(t, http.MethodPost, ts.URL+"/endpoints/00000000-0000-0000-0000-000000000000/secret/rotate", apiKey, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
