package worker

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"webhooks-go/internal/validation"
)

type ResolveAndPinResult struct {
	Allowed bool
	IP      string
	Reason  string
}

// Resolver looks up a hostname's addresses. The default is
// net.DefaultResolver.LookupIPAddr; tests inject a stub to control the
// answer without needing real DNS.
type Resolver func(ctx context.Context, hostname string) ([]net.IPAddr, error)

func defaultResolver(ctx context.Context, hostname string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, hostname)
}

// ResolveAndPin resolves the hostname exactly once, validates every
// resolved address against the shared denylist (internal/validation's
// CheckAddress — the same list registration-time validation uses), and pins
// exactly the address that got validated for the actual TCP connection
// (docs/adr/0006). A separate validate-then-connect lookup would leave a
// DNS-rebinding gap between the address that was checked and the address
// that gets dialed; resolving once and reusing that answer for both closes
// it.
func ResolveAndPin(ctx context.Context, hostname string, resolver Resolver) ResolveAndPinResult {
	if resolver == nil {
		resolver = defaultResolver
	}

	if ip := net.ParseIP(hostname); ip != nil {
		result := validation.CheckAddress(ip)
		if !result.Allowed {
			return ResolveAndPinResult{Allowed: false, Reason: result.Reason}
		}
		return ResolveAndPinResult{Allowed: true, IP: hostname}
	}

	addrs, err := resolver(ctx, hostname)
	if err != nil || len(addrs) == 0 {
		return ResolveAndPinResult{Allowed: false, Reason: "Could not resolve hostname"}
	}

	for _, addr := range addrs {
		check := validation.CheckAddress(addr.IP)
		if !check.Allowed {
			return ResolveAndPinResult{Allowed: false, Reason: check.Reason}
		}
	}

	return ResolveAndPinResult{Allowed: true, IP: addrs[0].IP.String()}
}

// pinnedIPContextKey carries the per-request pinned IP through to a shared
// *http.Transport's DialContext (see NewTransport) — a shared Transport
// gives real keep-alive pooling and a genuine per-host connection cap
// (MaxConnsPerHost, R-16) across every delivery, not just within one call,
// which a fresh Transport per call couldn't provide.
type pinnedIPContextKey struct{}

func withPinnedIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, pinnedIPContextKey{}, ip)
}

// NewTransport builds a Transport safe to share across concurrent
// SendOutboundRequest calls — each call's pinned IP travels via context,
// not via the Transport itself, so one instance can serve every delivery.
// maxConnsPerHost <= 0 means unlimited (used for SendOutboundRequest's own
// private one-shot Transport, where no cross-call pooling happens anyway).
func NewTransport(connectTimeoutMs, maxConnsPerHost int) *http.Transport {
	dialer := &net.Dialer{Timeout: time.Duration(connectTimeoutMs) * time.Millisecond}
	return &http.Transport{
		MaxConnsPerHost: maxConnsPerHost,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			pinnedIP, _ := ctx.Value(pinnedIPContextKey{}).(string)
			if pinnedIP == "" {
				return dialer.DialContext(ctx, network, addr)
			}
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				port = addr
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(pinnedIP, port))
		},
	}
}

type OutboundRequestOptions struct {
	Method               string
	Headers              map[string]string
	Body                 string
	ConnectTimeoutMs     int
	TotalTimeoutMs       int
	MaxResponseBodyBytes int
	// Transport, if set, is shared across calls for connection pooling and
	// R-16's per-host connection limit (build one via NewTransport). Nil
	// builds a private one-shot Transport for just this call.
	Transport *http.Transport
}

// The closed set attempts.error_class actually takes on at the network
// layer. Untyped string constants (not a named type) so the
// OutboundRequestResult.ErrorClass field stays a plain string, matching
// what's stored in Postgres and compared against in tests.
const (
	ErrClassConnectTimeout    = "connect_timeout"
	ErrClassTotalTimeout      = "total_timeout"
	ErrClassConnectionRefused = "connection_refused"
	ErrClassDNSError          = "dns_error"
	ErrClassConnectionReset   = "connection_reset"
	ErrClassConnectionError   = "connection_error"
)

type OutboundRequestResult struct {
	ResponseStatus        *int
	ResponseBodyTruncated string
	DurationMs            int64
	// Empty whenever a response was received, even a non-2xx one (R-16's
	// "2xx is success, everything else retries" is the delivery cycle's
	// decision, not this function's) — set only when no response ever
	// arrived.
	ErrorClass string
}

// SendOutboundRequest sends one delivery attempt to pinnedIP, never
// following redirects (docs/adr/0006 — a raw response is always the
// terminal outcome, 3xx included). u's hostname still drives the Host
// header and TLS SNI/certificate validation — only the TCP connection
// target is overridden, closing the DNS-rebinding gap between the address
// ResolveAndPin validated and the address actually dialed.
func SendOutboundRequest(ctx context.Context, pinnedIP string, u *url.URL, opts OutboundRequestOptions) OutboundRequestResult {
	start := time.Now()

	transport := opts.Transport
	ownTransport := transport == nil
	if ownTransport {
		transport = NewTransport(opts.ConnectTimeoutMs, 0)
		defer transport.CloseIdleConnections()
	}

	var connected atomic.Bool
	trace := &httptrace.ClientTrace{
		ConnectDone: func(network, addr string, err error) {
			if err == nil {
				connected.Store(true)
			}
		},
	}

	requestCtx := httptrace.WithClientTrace(withPinnedIP(ctx, pinnedIP), trace)
	requestCtx, cancel := context.WithTimeout(requestCtx, time.Duration(opts.TotalTimeoutMs)*time.Millisecond)
	defer cancel()

	client := &http.Client{
		Transport: transport,
		// Never follow redirects — a bounded hop count still doesn't bound
		// redirect destination, and not following any is simpler than
		// re-validating SSRF at every hop.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(requestCtx, opts.Method, u.String(), strings.NewReader(opts.Body))
	if err != nil {
		return OutboundRequestResult{DurationMs: time.Since(start).Milliseconds(), ErrorClass: ErrClassConnectionError}
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return classifyRequestError(err, connected.Load(), time.Since(start).Milliseconds())
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(opts.MaxResponseBodyBytes)))
	durationMs := time.Since(start).Milliseconds()
	status := resp.StatusCode

	if readErr != nil && errors.Is(readErr, context.DeadlineExceeded) {
		// The total timeout firing mid-body-read discards whatever partial
		// response was captured — matching the same "responseStatus: null"
		// outcome the pre-response timeout path reports, not a partial
		// success. A caller can't act on a response it never fully received
		// in time regardless of how much of it happened to already arrive.
		return OutboundRequestResult{DurationMs: durationMs, ErrorClass: ErrClassTotalTimeout}
	}
	// Any other body-read error (e.g. a mid-stream reset) is treated as a
	// completed response with whatever partial body arrived, not a failure
	// — only a deliberate timeout-triggered abort counts as an error
	// outcome here.
	return OutboundRequestResult{ResponseStatus: &status, ResponseBodyTruncated: string(body), DurationMs: durationMs}
}

func classifyRequestError(err error, connected bool, durationMs int64) OutboundRequestResult {
	var netErr net.Error
	isTimeout := errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
	if isTimeout {
		if connected {
			return OutboundRequestResult{DurationMs: durationMs, ErrorClass: ErrClassTotalTimeout}
		}
		return OutboundRequestResult{DurationMs: durationMs, ErrorClass: ErrClassConnectTimeout}
	}

	if errors.Is(err, syscall.ECONNREFUSED) {
		return OutboundRequestResult{DurationMs: durationMs, ErrorClass: ErrClassConnectionRefused}
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return OutboundRequestResult{DurationMs: durationMs, ErrorClass: ErrClassConnectionReset}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return OutboundRequestResult{DurationMs: durationMs, ErrorClass: ErrClassDNSError}
	}
	return OutboundRequestResult{DurationMs: durationMs, ErrorClass: ErrClassConnectionError}
}
