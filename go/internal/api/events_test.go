package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublishEventReturnsPendingExpansion(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := createTenant(t, pool)

	req, err := newJSONRequest(http.MethodPost, ts.URL+"/events", map[string]any{
		"type":    "order.created",
		"payload": map[string]any{"orderId": "abc123"},
	})
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Idempotency-Key", "publish-key-1")

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

func TestPublishEventIsIdempotentOnRepeatedKey(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := createTenant(t, pool)

	publish := func() (string, string) {
		req, err := newJSONRequest(http.MethodPost, ts.URL+"/events", map[string]any{
			"type":    "order.created",
			"payload": map[string]any{},
		})
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Idempotency-Key", "repeat-key")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusAccepted, resp.StatusCode)

		var body struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		decodeJSON(t, resp, &body)
		return body.ID, body.Status
	}

	firstID, firstStatus := publish()
	secondID, secondStatus := publish()
	require.Equal(t, firstID, secondID)
	require.Equal(t, firstStatus, secondStatus)
}

func TestPublishEventRejectsMissingIdempotencyKey(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := createTenant(t, pool)

	resp := doRequest(t, http.MethodPost, ts.URL+"/events", apiKey, map[string]any{
		"type":    "order.created",
		"payload": map[string]any{},
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// Matches node's z.record(z.unknown()): payload must be a JSON object.
// json.Unmarshal into a map silently treats JSON `null` as "no error, nil
// map" rather than rejecting it — this regression-tests the fix for that.
func TestPublishEventRejectsNullPayload(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := createTenant(t, pool)

	req, err := newJSONRequest(http.MethodPost, ts.URL+"/events", map[string]any{
		"type":    "order.created",
		"payload": nil,
	})
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Idempotency-Key", "null-payload-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPublishEventRejectsInvalidBody(t *testing.T) {
	pool := setupPool(t)
	ts := newTestServer(t, pool)
	_, apiKey := createTenant(t, pool)

	req, err := newJSONRequest(http.MethodPost, ts.URL+"/events", map[string]any{
		"payload": map[string]any{},
	})
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Idempotency-Key", "some-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
