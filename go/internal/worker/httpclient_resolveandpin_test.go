package worker_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"webhooks-go/internal/worker"
)

func TestResolveAndPinAllowsLiteralPublicIPv4(t *testing.T) {
	result := worker.ResolveAndPin(context.Background(), "93.184.216.34", nil)
	require.Equal(t, worker.ResolveAndPinResult{Allowed: true, IP: "93.184.216.34"}, result)
}

func TestResolveAndPinRejectsLiteralPrivateIPv4(t *testing.T) {
	result := worker.ResolveAndPin(context.Background(), "10.0.0.5", nil)
	require.False(t, result.Allowed)
}

func TestResolveAndPinRejectsIPv4MappedLoopbackLiteral(t *testing.T) {
	result := worker.ResolveAndPin(context.Background(), "::ffff:127.0.0.1", nil)
	require.False(t, result.Allowed)
}

func TestResolveAndPinResolvesViaInjectedResolverAndPinsValidatedAddress(t *testing.T) {
	called := ""
	resolver := func(ctx context.Context, hostname string) ([]net.IPAddr, error) {
		called = hostname
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}

	result := worker.ResolveAndPin(context.Background(), "example.com", resolver)

	require.Equal(t, worker.ResolveAndPinResult{Allowed: true, IP: "93.184.216.34"}, result)
	require.Equal(t, "example.com", called)
}

func TestResolveAndPinRejectsHostnameResolvingToPrivateAddress(t *testing.T) {
	resolver := func(ctx context.Context, hostname string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}

	result := worker.ResolveAndPin(context.Background(), "metadata.internal", resolver)
	require.False(t, result.Allowed)
}

// Resolves the hostname exactly once, so a rebinding resolver can't hand
// back a different address than the one that was validated — a second call
// would return a private address; proving a rebind attack only works if the
// implementation re-resolves, which it must not.
func TestResolveAndPinResolvesExactlyOnce(t *testing.T) {
	calls := 0
	resolver := func(ctx context.Context, hostname string) ([]net.IPAddr, error) {
		calls++
		if calls == 1 {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}

	result := worker.ResolveAndPin(context.Background(), "rebinding-host.example", resolver)

	require.Equal(t, 1, calls)
	require.Equal(t, worker.ResolveAndPinResult{Allowed: true, IP: "93.184.216.34"}, result)
}

func TestResolveAndPinRejectsUnresolvableHostname(t *testing.T) {
	resolver := func(ctx context.Context, hostname string) ([]net.IPAddr, error) {
		return nil, errors.New("no such host")
	}

	result := worker.ResolveAndPin(context.Background(), "does-not-resolve.invalid", resolver)
	require.False(t, result.Allowed)
}
