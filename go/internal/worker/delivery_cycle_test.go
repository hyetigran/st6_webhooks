package worker_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/testsupport"
	"webhooks-go/internal/worker"
)

var cycleConfig = worker.DeliveryConfig{
	SecretEncryptionKey: testsupport.SecretEncryptionKey,
	LeaseDurationMs:     leaseDurationMs,
	Outbound: worker.OutboundConfig{
		ConnectTimeoutMs:     2_000,
		TotalTimeoutMs:       2_000,
		MaxResponseBodyBytes: 65_536,
	},
	Backoff: worker.BackoffConfig{BaseDelayMs: 1_000, Multiplier: 2, MaxDelayMs: 30_000, MaxAttempts: 6},
}

// The real SSRF check (ResolveAndPin) always rejects loopback
// (docs/adr/0006) — correctly, since that's exactly the class of address it
// exists to block. A local test receiver is necessarily on loopback, so
// there is no DNS answer that both routes there and passes validation. This
// bypasses ResolveAndPin wholesale for orchestration tests; the real
// validate-then-pin logic has its own dedicated coverage in
// httpclient_resolveandpin_test.go and in the "rejects a private range"
// test below, which does not bypass it.
func trustPinnedLoopback(ctx context.Context, hostname string) worker.ResolveAndPinResult {
	return worker.ResolveAndPinResult{Allowed: true, IP: "127.0.0.1"}
}

func hmacSHA256Hex(secret, message string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestRunDeliveryCycleReturnsFalseWhenNothingToDeliver(t *testing.T) {
	pool := testsupport.SetupPool(t)
	did, err := worker.RunDeliveryCycle(context.Background(), pool, cycleConfig, worker.DeliveryCycleDeps{})
	require.NoError(t, err)
	require.False(t, did)
}

func TestRunDeliveryCycleEndToEndAgainstRealReceiver(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()

	var receivedHeaders http.Header
	var receivedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	_, port, err := net.SplitHostPort(ts.Listener.Addr().String())
	require.NoError(t, err)

	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{
		URL:           fmt.Sprintf("http://delivery-worker-test.invalid:%s/webhook", port),
		SigningSecret: "whsec_test_secret",
	})
	deliveryID, eventID := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{
		EventType: "order.created",
		Payload:   `{"orderId":"abc123"}`,
	})

	did, err := worker.RunDeliveryCycle(ctx, pool, cycleConfig, worker.DeliveryCycleDeps{ResolveAndPinFn: trustPinnedLoopback})
	require.NoError(t, err)
	require.True(t, did)

	require.JSONEq(t, `{"orderId":"abc123"}`, string(receivedBody))
	require.Equal(t, deliveryID, receivedHeaders.Get("Webhook-Id"))
	require.Equal(t, eventID, receivedHeaders.Get("Webhook-Event-Id"))
	require.Equal(t, "1", receivedHeaders.Get("Webhook-Attempt"))
	require.NotEmpty(t, receivedHeaders.Get("Webhook-Timestamp"))

	expectedSignature := hmacSHA256Hex("whsec_test_secret", receivedHeaders.Get("Webhook-Timestamp")+"."+string(receivedBody))
	require.Equal(t, expectedSignature, receivedHeaders.Get("Webhook-Signature"))

	var state string
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE id = $1", deliveryID).Scan(&state))
	require.Equal(t, "succeeded", state)
}

// docs/adr/0003: signs with every secret still inside its rotation-overlap
// window (current + secondary), not just the current one.
func TestRunDeliveryCycleSignsWithBothSecretsDuringRotationOverlap(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()

	var receivedHeaders http.Header
	var receivedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	_, port, err := net.SplitHostPort(ts.Listener.Addr().String())
	require.NoError(t, err)

	tenantID, _ := testsupport.CreateTenant(t, pool)
	future := time.Now().Add(60 * time.Second)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{
		URL:                      fmt.Sprintf("http://delivery-worker-test.invalid:%s/webhook", port),
		SigningSecret:            "whsec_new",
		SecondarySecret:          "whsec_old",
		SecondarySecretExpiresAt: &future,
	})
	testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{Payload: `{"orderId":"rotation-test"}`})

	_, err = worker.RunDeliveryCycle(ctx, pool, cycleConfig, worker.DeliveryCycleDeps{ResolveAndPinFn: trustPinnedLoopback})
	require.NoError(t, err)

	timestamp := receivedHeaders.Get("Webhook-Timestamp")
	message := timestamp + "." + string(receivedBody)
	expected := hmacSHA256Hex("whsec_new", message) + "," + hmacSHA256Hex("whsec_old", message)
	require.Equal(t, expected, receivedHeaders.Get("Webhook-Signature"))
}

func TestRunDeliveryCycleRejectsDeliveryToPrivateRangeWithoutConnecting(t *testing.T) {
	pool := testsupport.SetupPool(t)
	ctx := context.Background()

	tenantID, _ := testsupport.CreateTenant(t, pool)
	endpointID := testsupport.CreateEndpoint(t, pool, tenantID, []string{"order.created"}, testsupport.EndpointOptions{
		URL: "http://rebinding-host.invalid:1/webhook",
	})
	deliveryID, _ := testsupport.CreateDelivery(t, pool, tenantID, endpointID, testsupport.DeliveryOptions{})

	resolverCalls := 0
	rebindingResolver := func(ctx context.Context, hostname string) ([]net.IPAddr, error) {
		resolverCalls++
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}

	did, err := worker.RunDeliveryCycle(ctx, pool, cycleConfig, worker.DeliveryCycleDeps{
		ResolveAndPinFn: func(ctx context.Context, hostname string) worker.ResolveAndPinResult {
			return worker.ResolveAndPin(ctx, hostname, rebindingResolver)
		},
	})
	require.NoError(t, err)
	require.True(t, did) // a cycle ran — it just didn't succeed
	require.Equal(t, 1, resolverCalls)

	var state string
	require.NoError(t, pool.QueryRow(ctx, "SELECT state FROM deliveries WHERE id = $1", deliveryID).Scan(&state))
	require.Equal(t, "pending", state) // rescheduled for retry, not stuck in_flight

	var errorClass *string
	require.NoError(t, pool.QueryRow(ctx, "SELECT error_class FROM attempts WHERE delivery_id = $1", deliveryID).Scan(&errorClass))
	require.NotNil(t, errorClass)
	require.Equal(t, "url_not_allowed", *errorClass)
}
