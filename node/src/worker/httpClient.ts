import net, { isIP } from "node:net";
import { lookup as dnsLookup } from "node:dns/promises";
import http from "node:http";
import https from "node:https";
import { checkAddress } from "../validation/url.js";

export interface ResolveAndPinAllowed {
  allowed: true;
  ip: string;
}

export interface ResolveAndPinRejected {
  allowed: false;
  reason: string;
}

export type ResolveAndPinResult = ResolveAndPinAllowed | ResolveAndPinRejected;

export type Resolver = (hostname: string) => Promise<{ address: string; family: number }[]>;

const defaultResolver: Resolver = (hostname) => dnsLookup(hostname, { all: true });

// ADR-0006: resolve the hostname exactly once, validate every resolved
// address against the shared denylist (validation/url.ts's checkAddress —
// the same list registration-time validation uses), and pin exactly the
// address that got validated for the actual TCP connection. A separate
// validate-then-connect lookup would leave a DNS-rebinding gap between the
// address that was checked and the address that gets dialed; resolving once
// and reusing that answer for both closes it.
export async function resolveAndPin(hostname: string, resolver: Resolver = defaultResolver): Promise<ResolveAndPinResult> {
  const literalVersion = isIP(hostname);
  if (literalVersion !== 0) {
    const result = checkAddress(hostname);
    return result.allowed ? { allowed: true, ip: hostname } : { allowed: false, reason: result.reason ?? "Address not allowed" };
  }

  let resolved: { address: string; family: number }[];
  try {
    resolved = await resolver(hostname);
  } catch {
    return { allowed: false, reason: "Could not resolve hostname" };
  }

  if (resolved.length === 0) {
    return { allowed: false, reason: "Could not resolve hostname" };
  }

  for (const { address } of resolved) {
    const check = checkAddress(address);
    if (!check.allowed) return { allowed: false, reason: check.reason ?? "Address not allowed" };
  }

  return { allowed: true, ip: resolved[0]!.address };
}

// Node's net module calls the custom `lookup` option with `{ all: true }`
// (Happy Eyeballs, since Node 18), which expects the array-form callback
// rather than the classic single-address (err, address, family) form.
function pinnedLookup(pinnedIp: string, pinnedFamily: number): net.LookupFunction {
  return (_hostname, options, callback) => {
    if (options.all) {
      callback(null, [{ address: pinnedIp, family: pinnedFamily }]);
    } else {
      callback(null, pinnedIp, pinnedFamily);
    }
  };
}

export interface OutboundRequestOptions {
  method: string;
  headers: Record<string, string>;
  body: string;
  connectTimeoutMs: number;
  totalTimeoutMs: number;
  maxResponseBodyBytes: number;
  agent?: http.Agent | https.Agent;
}

// The closed set attempts.error_class actually takes on at the network
// layer — a union instead of a bare string so a typo'd class name is a
// compile error at every call site, not a silent drift.
export type NetworkErrorClass =
  | "connect_timeout"
  | "total_timeout"
  | "connection_refused"
  | "dns_error"
  | "connection_reset"
  | "connection_error";

export interface OutboundRequestResult {
  responseStatus: number | null;
  responseBodyTruncated: string;
  durationMs: number;
  // null whenever a response was received, even a non-2xx one (R-16's "2xx
  // is success, everything else retries" is a delivery-cycle decision, not
  // this function's) — set only when no response ever arrived.
  errorClass: NetworkErrorClass | null;
}

// ADR-0006: redirects are never followed — a raw http.request response is
// always the terminal outcome, 3xx included, so there's no follow-up logic
// to write. The custom `lookup` pins the connection to the address
// resolveAndPin already validated; `hostname`/`servername` stay the
// original hostname so the Host header and TLS SNI are unaffected.
export async function sendOutboundRequest(
  pinnedIp: string,
  url: URL,
  options: OutboundRequestOptions,
): Promise<OutboundRequestResult> {
  const isHttps = url.protocol === "https:";
  const transport = isHttps ? https : http;
  const startedAt = Date.now();
  const pinnedFamily = isIP(pinnedIp) === 6 ? 6 : 4;

  return new Promise((resolve) => {
    let settled = false;
    let connectTimer: NodeJS.Timeout;
    let totalTimer: NodeJS.Timeout;

    const finish = (result: OutboundRequestResult): void => {
      if (settled) return;
      settled = true;
      clearTimeout(connectTimer);
      clearTimeout(totalTimer);
      resolve(result);
    };

    const req = transport.request({
      method: options.method,
      hostname: url.hostname,
      port: url.port || (isHttps ? 443 : 80),
      path: url.pathname + url.search,
      headers: options.headers,
      agent: options.agent,
      servername: isHttps ? url.hostname : undefined,
      // Node's net module calls this with `{ all: true }` (Happy Eyeballs,
      // since Node 18), which expects the array-form callback rather than
      // the classic single-address (err, address, family) form — has to
      // handle both since which one Node requests isn't part of the
      // documented contract we control.
      lookup: pinnedLookup(pinnedIp, pinnedFamily),
    });

    connectTimer = setTimeout(() => {
      req.destroy();
      finish({ responseStatus: null, responseBodyTruncated: "", durationMs: Date.now() - startedAt, errorClass: "connect_timeout" });
    }, options.connectTimeoutMs);

    totalTimer = setTimeout(() => {
      req.destroy();
      finish({ responseStatus: null, responseBodyTruncated: "", durationMs: Date.now() - startedAt, errorClass: "total_timeout" });
    }, options.totalTimeoutMs);

    req.once("socket", (socket) => {
      socket.once(isHttps ? "secureConnect" : "connect", () => clearTimeout(connectTimer));
    });

    req.once("error", (err: NodeJS.ErrnoException) => {
      finish({ responseStatus: null, responseBodyTruncated: "", durationMs: Date.now() - startedAt, errorClass: classifyError(err) });
    });

    req.once("response", (res) => {
      let received = Buffer.alloc(0);

      res.on("data", (chunk: Buffer) => {
        if (settled) return;
        if (received.length + chunk.length > options.maxResponseBodyBytes) {
          received = Buffer.concat([received, chunk]).subarray(0, options.maxResponseBodyBytes);
          res.destroy();
          finish({
            responseStatus: res.statusCode ?? null,
            responseBodyTruncated: received.toString("utf8"),
            durationMs: Date.now() - startedAt,
            errorClass: null,
          });
          return;
        }
        received = Buffer.concat([received, chunk]);
      });

      const finishWithReceivedBody = (): void => {
        finish({
          responseStatus: res.statusCode ?? null,
          responseBodyTruncated: received.toString("utf8"),
          durationMs: Date.now() - startedAt,
          errorClass: null,
        });
      };
      res.on("end", finishWithReceivedBody);
      res.on("error", finishWithReceivedBody);
    });

    req.end(options.body);
  });
}

function classifyError(err: NodeJS.ErrnoException): NetworkErrorClass {
  if (err.code === "ECONNREFUSED") return "connection_refused";
  if (err.code === "ENOTFOUND") return "dns_error";
  if (err.code === "ECONNRESET") return "connection_reset";
  return "connection_error";
}
