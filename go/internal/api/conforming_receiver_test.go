package api_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/testsupport"
	"webhooks-go/internal/worker"
)

// receiverPort extracts the httptest server's bound port. Registration uses
// a real, DNS-resolvable hostname ("example.com") with this port rather
// than the server's own 127.0.0.1 URL — registration-time SSRF validation
// has no resolver override, so it needs a hostname that genuinely resolves
// to a public address; the delivery cycle's injected resolver
// (trustLoopback) is what actually redirects the real connection to
// 127.0.0.1 regardless of what the URL's hostname resolves to.
func receiverPort(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	require.NoError(t, err)
	return port
}

func trustLoopback(ctx context.Context, hostname string) worker.ResolveAndPinResult {
	return worker.ResolveAndPinResult{Allowed: true, IP: "127.0.0.1"}
}

var conformingReceiverCycleConfig = worker.DeliveryConfig{
	SecretEncryptionKey: testsupport.SecretEncryptionKey,
	LeaseDurationMs:     60_000,
	Outbound: worker.OutboundConfig{
		ConnectTimeoutMs:     2_000,
		TotalTimeoutMs:       2_000,
		MaxResponseBodyBytes: 65_536,
	},
	Backoff: worker.BackoffConfig{BaseDelayMs: 10, Multiplier: 2, MaxDelayMs: 100, MaxAttempts: 6},
}

// REVIEW.md F-13 / PRD §6: a conforming receiver dedupes on *successfully
// processed* event_id, not on event_id merely seen. This is the property
// that makes replaying a previously-*failed* event actually work; a
// receiver that (incorrectly) marked "seen" at attempt time would silently
// no-op that replay, defeating the reason to replay it in the first place.
func TestConformingReceiverReplayOfSucceededEventIsNoOp(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	ts := newTestServer(t, pool)

	attemptToSucceedAt := 2
	receiver := testsupport.NewConformingReceiver(func(_ string, attemptNumber int) bool { return attemptNumber >= attemptToSucceedAt })
	receiverServer := httptest.NewServer(http.HandlerFunc(receiver.Handler))
	defer receiverServer.Close()

	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	registerResp := doRequest(t, http.MethodPost, ts.URL+"/endpoints", apiKey, map[string]any{
		"url":         "http://example.com:" + receiverPort(t, receiverServer) + "/hook",
		"event_types": []string{"order.created"},
	})
	require.Equal(t, http.StatusCreated, registerResp.StatusCode)
	var registered struct {
		ID string `json:"id"`
	}
	decodeJSON(t, registerResp, &registered)
	endpointID := registered.ID

	_, eventID := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{})

	did, err := worker.RunDeliveryCycle(ctx, pool, conformingReceiverCycleConfig, worker.DeliveryCycleDeps{ResolveAndPinFn: trustLoopback}) // attempt 1: fails
	require.NoError(t, err)
	require.True(t, did)
	time.Sleep(150 * time.Millisecond)                                                                                                     // past the small injected backoff delay
	did, err = worker.RunDeliveryCycle(ctx, pool, conformingReceiverCycleConfig, worker.DeliveryCycleDeps{ResolveAndPinFn: trustLoopback}) // attempt 2: succeeds
	require.NoError(t, err)
	require.True(t, did)

	require.True(t, receiver.HasProcessed(eventID))
	require.Equal(t, 2, receiver.AttemptCount(eventID))

	req, err := newJSONRequest(http.MethodPost, ts.URL+"/endpoints/"+endpointID+"/replays", map[string]any{
		"range_start": "2020-01-01T00:00:00.000Z",
		"range_end":   "2030-01-01T00:00:00.000Z",
	})
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Idempotency-Key", "replay-of-succeeded")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	_, err = worker.RunReplayExpansionCycle(ctx, pool)
	require.NoError(t, err)
	_, err = worker.RunDeliveryCycle(ctx, pool, conformingReceiverCycleConfig, worker.DeliveryCycleDeps{ResolveAndPinFn: trustLoopback})
	require.NoError(t, err)

	// The receiver never re-ran its business logic — attempt count for this
	// event_id is unchanged, even though a real HTTP request reached it.
	require.Equal(t, 2, receiver.AttemptCount(eventID))

	var state string
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE event_id = $1 ORDER BY seq DESC LIMIT 1", eventID).Scan(&state))
	require.Equal(t, "succeeded", state)
}

func TestConformingReceiverReplayOfNeverSucceededEventIsReprocessed(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()
	ts := newTestServer(t, pool)

	allowSuccess := false
	receiver := testsupport.NewConformingReceiver(func(_ string, _ int) bool { return allowSuccess })
	receiverServer := httptest.NewServer(http.HandlerFunc(receiver.Handler))
	defer receiverServer.Close()

	tenantID, apiKey := testsupport.CreateTenant(t, pool)
	registerResp := doRequest(t, http.MethodPost, ts.URL+"/endpoints", apiKey, map[string]any{
		"url":         "http://example.com:" + receiverPort(t, receiverServer) + "/hook",
		"event_types": []string{"order.created"},
	})
	require.Equal(t, http.StatusCreated, registerResp.StatusCode)
	var registered struct {
		ID string `json:"id"`
	}
	decodeJSON(t, registerResp, &registered)
	endpointID := registered.ID

	_, eventID := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{})

	// Exhausts the attempt ceiling — the receiver never succeeds, so it's
	// never marked processed.
	for i := 0; i < conformingReceiverCycleConfig.Backoff.MaxAttempts; i++ {
		_, err := worker.RunDeliveryCycle(ctx, pool, conformingReceiverCycleConfig, worker.DeliveryCycleDeps{ResolveAndPinFn: trustLoopback})
		require.NoError(t, err)
		time.Sleep(150 * time.Millisecond)
	}
	require.False(t, receiver.HasProcessed(eventID))

	var status string
	require.NoError(t, pool.QueryRow(ctx, "SELECT status FROM endpoints WHERE id = $1", endpointID).Scan(&status))
	require.Equal(t, "halted", status)

	resumeResp := doRequest(t, http.MethodPost, ts.URL+"/endpoints/"+endpointID+"/resume", apiKey, nil)
	require.Equal(t, http.StatusOK, resumeResp.StatusCode)

	// "Someone fixes the downstream bug" — now it can actually succeed.
	allowSuccess = true
	req, err := newJSONRequest(http.MethodPost, ts.URL+"/endpoints/"+endpointID+"/replays", map[string]any{
		"range_start": "2020-01-01T00:00:00.000Z",
		"range_end":   "2030-01-01T00:00:00.000Z",
	})
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Idempotency-Key", "replay-of-failed")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	_, err = worker.RunReplayExpansionCycle(ctx, pool)
	require.NoError(t, err)
	_, err = worker.RunDeliveryCycle(ctx, pool, conformingReceiverCycleConfig, worker.DeliveryCycleDeps{ResolveAndPinFn: trustLoopback})
	require.NoError(t, err)

	// A conforming receiver actually ran its business logic again — a
	// receiver that (incorrectly) deduped on mere receipt would never have
	// reached this point, since it would have "seen" this event_id already
	// during the attempts above.
	require.True(t, receiver.HasProcessed(eventID))

	var state string
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE event_id = $1 ORDER BY seq DESC LIMIT 1", eventID).Scan(&state))
	require.Equal(t, "succeeded", state)
}
