package worker_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/worker"
)

func baseOptions() worker.OutboundRequestOptions {
	return worker.OutboundRequestOptions{
		Method:               http.MethodPost,
		Headers:              map[string]string{"content-type": "application/json"},
		Body:                 `{"hello":"world"}`,
		ConnectTimeoutMs:     2_000,
		TotalTimeoutMs:       2_000,
		MaxResponseBodyBytes: 1_000,
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

// serverPort extracts the port httptest bound to, so tests can build a URL
// with an arbitrary hostname pointing at the real listening loopback server
// — mirroring node/test/testServer.ts's pattern of separating "what the
// request is addressed to" from "where it's actually pinned to connect."
func serverPort(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	require.NoError(t, err)
	return port
}

func TestSendOutboundRequestReturnsStatusAndBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	result := worker.SendOutboundRequest(context.Background(), "127.0.0.1",
		mustURL(t, fmt.Sprintf("http://original-hostname.test:%s/webhook", serverPort(t, ts))), baseOptions())

	require.NotNil(t, result.ResponseStatus)
	require.Equal(t, 200, *result.ResponseStatus)
	require.Equal(t, "ok", result.ResponseBodyTruncated)
	require.Empty(t, result.ErrorClass)
}

func TestSendOutboundRequestSetsHostHeaderToOriginalHostname(t *testing.T) {
	var receivedHost string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	port := serverPort(t, ts)

	worker.SendOutboundRequest(context.Background(), "127.0.0.1",
		mustURL(t, fmt.Sprintf("http://original-hostname.test:%s/webhook", port)), baseOptions())

	require.Equal(t, fmt.Sprintf("original-hostname.test:%s", port), receivedHost)
}

func TestSendOutboundRequestTreats3xxAsTerminalNotFollowed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://169.254.169.254/latest/meta-data")
		w.WriteHeader(http.StatusFound)
	}))
	defer ts.Close()

	result := worker.SendOutboundRequest(context.Background(), "127.0.0.1",
		mustURL(t, fmt.Sprintf("http://original-hostname.test:%s/webhook", serverPort(t, ts))), baseOptions())

	require.NotNil(t, result.ResponseStatus)
	require.Equal(t, 302, *result.ResponseStatus)
	require.Empty(t, result.ErrorClass)
}

func TestSendOutboundRequestCapsResponseBodyWithoutError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strings.Repeat("x", 5_000)))
	}))
	defer ts.Close()

	opts := baseOptions()
	opts.MaxResponseBodyBytes = 100
	result := worker.SendOutboundRequest(context.Background(), "127.0.0.1",
		mustURL(t, fmt.Sprintf("http://original-hostname.test:%s/webhook", serverPort(t, ts))), opts)

	require.Len(t, result.ResponseBodyTruncated, 100)
	require.NotNil(t, result.ResponseStatus)
	require.Equal(t, 200, *result.ResponseStatus)
	require.Empty(t, result.ErrorClass)
}

func TestSendOutboundRequestTotalTimeoutOnSlowResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("too slow"))
	}))
	defer ts.Close()

	opts := baseOptions()
	opts.TotalTimeoutMs = 100
	result := worker.SendOutboundRequest(context.Background(), "127.0.0.1",
		mustURL(t, fmt.Sprintf("http://original-hostname.test:%s/webhook", serverPort(t, ts))), opts)

	require.Nil(t, result.ResponseStatus)
	require.Equal(t, "total_timeout", result.ErrorClass)
}

// R-16: a receiver that starts responding (headers + first byte arrive
// immediately) but then trickles the body forever without ever finishing
// must still be bounded by the total timeout, not just a "response never
// started" timeout.
func TestSendOutboundRequestTotalTimeoutOnSlowLorisReceiver(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("a"))
		if ok {
			flusher.Flush()
		}
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; i < 50; i++ {
			<-ticker.C
			if _, err := w.Write([]byte("b")); err != nil {
				return
			}
			if ok {
				flusher.Flush()
			}
		}
	}))
	defer ts.Close()

	opts := baseOptions()
	opts.TotalTimeoutMs = 100
	result := worker.SendOutboundRequest(context.Background(), "127.0.0.1",
		mustURL(t, fmt.Sprintf("http://original-hostname.test:%s/webhook", serverPort(t, ts))), opts)

	require.Nil(t, result.ResponseStatus)
	require.Equal(t, "total_timeout", result.ErrorClass)
}

func TestSendOutboundRequestConnectionRefusedWhenNothingListening(t *testing.T) {
	result := worker.SendOutboundRequest(context.Background(), "127.0.0.1",
		mustURL(t, "http://original-hostname.test:1/webhook"), baseOptions())

	require.Nil(t, result.ResponseStatus)
	require.Equal(t, "connection_refused", result.ErrorClass)
}
