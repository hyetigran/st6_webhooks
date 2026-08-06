import { isIP } from "node:net";
import { lookup } from "node:dns/promises";

export interface UrlValidationResult {
  allowed: boolean;
  reason?: string;
}

// R-2: registration rejects URLs that resolve to private, loopback, or
// link-local ranges. This check runs at registration time; the delivery
// worker re-validates at send time since DNS can change afterward.
export async function validateEndpointUrl(rawUrl: string): Promise<UrlValidationResult> {
  let parsed: URL;
  try {
    parsed = new URL(rawUrl);
  } catch {
    return { allowed: false, reason: "Not a valid URL" };
  }

  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return { allowed: false, reason: "URL must use http or https" };
  }

  const hostname = parsed.hostname;
  const literalVersion = isIP(hostname);
  if (literalVersion !== 0) {
    return checkAddress(hostname);
  }

  let resolved: { address: string; family: number }[];
  try {
    resolved = await lookup(hostname, { all: true });
  } catch {
    return { allowed: false, reason: "Could not resolve hostname" };
  }

  for (const { address } of resolved) {
    const result = checkAddress(address);
    if (!result.allowed) return result;
  }

  return { allowed: true };
}

// Exported so the delivery worker (ADR-0006, docs/adr/) can validate a
// resolved address against the exact same denylist it pins for the actual
// outbound connection — one shared list, not two that could drift apart.
export function checkAddress(address: string): UrlValidationResult {
  if (isIP(address) === 4) {
    return checkIpv4(address);
  }
  return checkIpv6(address);
}

function checkIpv4(address: string): UrlValidationResult {
  const octets = address.split(".").map(Number);
  const [a, b] = octets as [number, number, number, number];

  if (a === 127) return { allowed: false, reason: "Loopback address (127.0.0.0/8)" };
  if (a === 10) return { allowed: false, reason: "Private address (10.0.0.0/8)" };
  if (a === 172 && b >= 16 && b <= 31) return { allowed: false, reason: "Private address (172.16.0.0/12)" };
  if (a === 192 && b === 168) return { allowed: false, reason: "Private address (192.168.0.0/16)" };
  if (a === 169 && b === 254) return { allowed: false, reason: "Link-local address (169.254.0.0/16)" };
  if (a === 0) return { allowed: false, reason: "Unspecified address (0.0.0.0/8)" };
  if (a === 100 && b >= 64 && b <= 127) return { allowed: false, reason: "Carrier-grade NAT address (100.64.0.0/10)" };

  return { allowed: true };
}

function checkIpv6(address: string): UrlValidationResult {
  const normalized = address.toLowerCase();

  if (normalized === "::1") return { allowed: false, reason: "Loopback address (::1)" };
  if (normalized.startsWith("fe80:") || normalized.startsWith("fe8") || normalized.startsWith("fe9")
    || normalized.startsWith("fea") || normalized.startsWith("feb")) {
    return { allowed: false, reason: "Link-local address (fe80::/10)" };
  }
  if (normalized.startsWith("fc") || normalized.startsWith("fd")) {
    return { allowed: false, reason: "Unique local address (fc00::/7)" };
  }

  // IPv4-mapped IPv6 (::ffff:a.b.c.d) — check the embedded IPv4 address too.
  const mapped = normalized.match(/^::ffff:(\d+\.\d+\.\d+\.\d+)$/);
  if (mapped) {
    return checkIpv4(mapped[1]!);
  }

  return { allowed: true };
}
