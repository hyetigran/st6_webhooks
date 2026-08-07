// Package validation implements R-2: registered/delivery URLs must not
// resolve to a private, loopback, link-local, or carrier-grade-NAT address
// (SSRF defense). Mirrors node/src/validation/url.ts's checks exactly so the
// two stacks reject/accept the identical set of addresses.
package validation

import (
	"context"
	"net"
	"net/url"
	"strings"
)

type URLValidationResult struct {
	Allowed bool
	Reason  string
}

func allowed() URLValidationResult { return URLValidationResult{Allowed: true} }

func denied(reason string) URLValidationResult {
	return URLValidationResult{Allowed: false, Reason: reason}
}

// ValidateEndpointURL runs at registration time; the delivery worker
// re-validates at send time since DNS can change afterward (docs/adr/0006).
func ValidateEndpointURL(ctx context.Context, rawURL string) URLValidationResult {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return denied("Not a valid URL")
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return denied("URL must use http or https")
	}

	hostname := parsed.Hostname()
	if ip := net.ParseIP(hostname); ip != nil {
		return CheckAddress(ip)
	}

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil || len(addrs) == 0 {
		return denied("Could not resolve hostname")
	}

	for _, addr := range addrs {
		if result := CheckAddress(addr.IP); !result.Allowed {
			return result
		}
	}

	return allowed()
}

// CheckAddress is exported so the delivery worker (docs/adr/0006) can
// validate a resolved address against the exact same denylist it pins for
// the actual outbound connection — one shared list, not two that could
// drift apart.
func CheckAddress(ip net.IP) URLValidationResult {
	if v4 := ip.To4(); v4 != nil {
		return checkIPv4(v4)
	}
	return checkIPv6(ip)
}

func checkIPv4(ip net.IP) URLValidationResult {
	a, b := int(ip[0]), int(ip[1])

	switch {
	case a == 127:
		return denied("Loopback address (127.0.0.0/8)")
	case a == 10:
		return denied("Private address (10.0.0.0/8)")
	case a == 172 && b >= 16 && b <= 31:
		return denied("Private address (172.16.0.0/12)")
	case a == 192 && b == 168:
		return denied("Private address (192.168.0.0/16)")
	case a == 169 && b == 254:
		return denied("Link-local address (169.254.0.0/16)")
	case a == 0:
		return denied("Unspecified address (0.0.0.0/8)")
	case a == 100 && b >= 64 && b <= 127:
		return denied("Carrier-grade NAT address (100.64.0.0/10)")
	}

	return allowed()
}

func checkIPv6(ip net.IP) URLValidationResult {
	normalized := strings.ToLower(ip.String())

	if normalized == "::1" {
		return denied("Loopback address (::1)")
	}
	if strings.HasPrefix(normalized, "fe8") || strings.HasPrefix(normalized, "fe9") ||
		strings.HasPrefix(normalized, "fea") || strings.HasPrefix(normalized, "feb") {
		return denied("Link-local address (fe80::/10)")
	}
	if strings.HasPrefix(normalized, "fc") || strings.HasPrefix(normalized, "fd") {
		return denied("Unique local address (fc00::/7)")
	}

	// Note: unlike node/src/validation/url.ts, no separate IPv4-mapped
	// (::ffff:a.b.c.d) check is needed here — Go's net.IP.To4() already
	// recognizes that form and CheckAddress routes it to checkIPv4 before
	// ever reaching this function.

	return allowed()
}
