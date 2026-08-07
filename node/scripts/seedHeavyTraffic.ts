// One-off dev-data seeding script — NOT part of `make verify`/test/chaos/
// load, never shipped. Populates the REAL local dev database (whatever
// DATABASE_URL .env points at — the same one `npm run dev`/`npm run
// dev:worker` use) with a large, varied volume of endpoints/events/
// deliveries so the console dashboard has something interesting to show
// instead of near-empty demo data.
//
// Goes through the real pipeline end to end (real publish-shaped row
// inserts, real expansion, real HTTP delivery through the real worker
// cycles) rather than fabricating delivery outcomes directly — every
// attempt row that lands has a real HTTP round trip and real backoff/lease/
// ordering logic behind it. The one necessary exception is the same one
// chaos/worker-entrypoint.ts already established for exactly this reason: a
// local receiver needs the real, correct SSRF check bypassed, since
// production code is right to reject loopback addresses and shouldn't be
// weakened for seeding convenience. This script reuses that exact
// entrypoint unmodified, spawned several times over for real cross-endpoint
// concurrency — delivery is single-flight per endpoint by design (R-2), so
// throughput scales with the number of distinct *active* endpoints making
// progress at once, not with worker-process count.
//
// IMPORTANT: run this with the real `npm run dev:worker` stopped first — it
// enforces the real SSRF check and would race this script's permissive
// workers for claims, polluting the intended per-endpoint success/fail mix
// with occasional real SSRF-rejection attempts. Restart it after this
// script finishes (`npm run dev:worker`).
//
// Usage: npx tsx scripts/seedHeavyTraffic.ts [apiKey]
// apiKey defaults to whatever DEMO_API_KEY below is set to — pass the key
// your browser is actually signed in with (see the frontend's
// localStorage["gauntlet-relay:api-key"]) so the seeded data shows up
// without switching keys.
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { spawn, type ChildProcess } from "node:child_process";
import { pool } from "../src/db/pool.js";
import { encryptSecret, hashApiKey } from "../src/lib/crypto.js";
import { nodeDir, tsxNodeArgs, killProcess } from "./scenarioHarness.js";

const DEMO_API_KEY = "tenant_efc3135881cbe7d93fa828c4dc0219558d0440e1fe48590c";
const apiKey = process.argv[2] ?? DEMO_API_KEY;

const RECEIVER_PORT = 4900;
const RECEIVER_BASE = `http://127.0.0.1:${RECEIVER_PORT}`;
const WORKER_PROCESS_COUNT = 6;
// Bounded, not "until fully drained" — see waitForExpansionDone's comment.
const DELIVERY_WINDOW_MS = 8 * 60 * 1000;

interface EndpointProfile {
  key: string;
  path: string;
  eventTypes: string[];
  paused?: boolean;
}

// The mix: five endpoints that just work (the bulk of the volume), one a
// bit slower, one flaky-then-succeeds probabilistically, one that
// deterministically needs exactly 3 attempts (via the real `webhook-attempt`
// header the worker already sends), one that always fails (halts after its
// first delivery exhausts attempts — R-11 head-of-line ordering then freezes
// every delivery queued behind it, which is the interesting part), and one
// paused outright (its queue never gets touched at all).
const PROFILES: EndpointProfile[] = [
  { key: "billing", path: "/hooks/billing", eventTypes: ["order.placed", "invoice.paid"] },
  { key: "inventory", path: "/hooks/inventory", eventTypes: ["order.placed"] },
  { key: "shipping", path: "/hooks/shipping", eventTypes: ["order.placed", "invoice.paid"] },
  { key: "notifications", path: "/hooks/notifications", eventTypes: ["order.placed"] },
  { key: "crm", path: "/hooks/crm", eventTypes: ["crm.contact-synced"] },
  { key: "analytics", path: "/hooks/analytics", eventTypes: ["analytics.pageview-batch"] },
  { key: "fraud", path: "/hooks/fraud", eventTypes: ["fraud.review-requested"] },
  { key: "legacy", path: "/hooks/legacy", eventTypes: ["order.placed"] },
  { key: "reporting", path: "/hooks/reporting", eventTypes: ["reports.nightly-summary"] },
  { key: "partner", path: "/hooks/partner", eventTypes: ["order.placed"], paused: true },
];

// One shared stream (order.placed) fans out to six endpoints — this is
// where the bulk of the 100k+ deliveries comes from cheaply, since fan-out
// multiplies delivery rows without multiplying the (serialized-per-tenant)
// expansion cost. The rest are small, dedicated streams sized to keep their
// endpoint's own serial queue (single-flight per endpoint, R-2) from
// dominating total wall-clock time.
const EVENT_STREAMS: { type: string; count: number; payload: (i: number) => unknown }[] = [
  {
    type: "order.placed",
    count: 17_000,
    payload: (i) => ({ order_id: `ord_${i}`, amount_cents: 500 + (i % 9000), currency: "USD" }),
  },
  {
    type: "invoice.paid",
    count: 3_000,
    payload: (i) => ({ invoice_id: `inv_${i}`, amount_cents: 1000 + (i % 20000), currency: "USD" }),
  },
  { type: "crm.contact-synced", count: 800, payload: (i) => ({ contact_id: `cnt_${i}`, source: "webform" }) },
  { type: "analytics.pageview-batch", count: 600, payload: (i) => ({ batch_id: `batch_${i}`, pageviews: 40 + (i % 200) }) },
  { type: "fraud.review-requested", count: 600, payload: (i) => ({ review_id: `rev_${i}`, risk_score: (i % 100) / 100 }) },
  { type: "reports.nightly-summary", count: 40, payload: (i) => ({ report_id: `rpt_${i}`, rows: 1000 + i * 37 }) },
];

function log(msg: string): void {
  console.log(`[seed] ${new Date().toISOString().slice(11, 19)} ${msg}`);
}

async function findTenant(): Promise<string> {
  const { rows } = await pool.query<{ id: string }>("SELECT id FROM tenants WHERE api_key_hash = $1", [hashApiKey(apiKey)]);
  const tenant = rows[0];
  if (!tenant) {
    throw new Error(
      `No tenant found for the given API key. Pass the key your browser is signed in with as the first argument, ` +
        `or check localStorage["gauntlet-relay:api-key"] in the app.`,
    );
  }
  return tenant.id;
}

async function insertEndpoints(tenantId: string): Promise<Map<string, string>> {
  const encryptedSecret = encryptSecret("whsec_seed_heavy_traffic");
  const ids = new Map<string, string>();
  for (const p of PROFILES) {
    const url = `${RECEIVER_BASE}${p.path}`;
    const { rows } = await pool.query<{ id: string }>(
      `INSERT INTO endpoints (tenant_id, url, event_types, signing_secret, status)
       VALUES ($1, $2, $3, $4, $5) RETURNING id`,
      [tenantId, url, p.eventTypes, encryptedSecret, p.paused ? "paused" : "active"],
    );
    ids.set(p.key, rows[0]!.id);
    log(`registered ${p.key} -> ${url}${p.paused ? " (paused)" : ""}`);
  }
  return ids;
}

async function insertEventsBatch(tenantId: string, type: string, keyPrefix: string, start: number, count: number, payloadFn: (i: number) => unknown): Promise<void> {
  const values: string[] = [];
  const params: unknown[] = [];
  for (let i = start; i < start + count; i++) {
    params.push(tenantId, `${keyPrefix}-${i}`, type, JSON.stringify(payloadFn(i)));
    const base = params.length - 4;
    values.push(`($${base + 1}, $${base + 2}, $${base + 3}, $${base + 4})`);
  }
  await pool.query(`INSERT INTO events (tenant_id, idempotency_key, type, payload) VALUES ${values.join(",")}`, params);
}

async function insertEventStream(tenantId: string, stream: (typeof EVENT_STREAMS)[number]): Promise<void> {
  const BATCH = 1000;
  const keyPrefix = `seed-${stream.type}`;
  for (let start = 0; start < stream.count; start += BATCH) {
    const n = Math.min(BATCH, stream.count - start);
    await insertEventsBatch(tenantId, stream.type, keyPrefix, start, n, stream.payload);
  }
  log(`published ${stream.count} × ${stream.type}`);
}

// The local receiver every seeded endpoint's url points at. Behavior is
// keyed by path, one per endpoint profile above — the real worker's
// `webhook-attempt` header (delivery.ts) is what lets `fraud` deterministically
// need exactly 3 attempts rather than relying on randomness for that one.
function startReceiver(): ReturnType<typeof createServer> {
  const respond = (res: ServerResponse, status: number, body = "{}"): void => {
    res.writeHead(status, { "content-type": "application/json" });
    res.end(body);
  };

  const server = createServer((req: IncomingMessage, res: ServerResponse) => {
    // Drain the body — the worker doesn't wait for a receiver to read it
    // before it can send further requests, but leaving it unconsumed leaks
    // sockets under this much volume.
    req.resume();

    const path = req.url ?? "";
    const attempt = Number(req.headers["webhook-attempt"] ?? "1");

    if (path.startsWith("/hooks/legacy")) {
      return respond(res, 500, '{"error":"upstream unavailable"}');
    }
    if (path.startsWith("/hooks/fraud")) {
      return attempt < 3 ? respond(res, 503, '{"error":"rate limited, try again"}') : respond(res, 200);
    }
    if (path.startsWith("/hooks/analytics")) {
      return Math.random() < 0.2 ? respond(res, 503, '{"error":"batch processor busy"}') : respond(res, 200);
    }
    if (path.startsWith("/hooks/reporting")) {
      return void setTimeout(() => respond(res, 200), 2000 + Math.random() * 1500);
    }
    if (path.startsWith("/hooks/crm")) {
      return void setTimeout(() => respond(res, 200), 100 + Math.random() * 150);
    }
    // billing/inventory/shipping/notifications/partner: fast success.
    return void setTimeout(() => respond(res, 200), 2 + Math.random() * 8);
  });

  return server;
}

function spawnDeliveryWorker(): ChildProcess {
  return spawn(process.execPath, [...tsxNodeArgs, "chaos/worker-entrypoint.ts"], {
    cwd: nodeDir,
    env: { ...process.env, WORKER_IDLE_POLL_INTERVAL_MS: "20" },
    stdio: ["ignore", "ignore", "pipe"],
  });
}

interface ProgressCounts {
  pendingExpansion: number;
  claimablePending: number;
  inFlight: number;
  succeeded: number;
  failed: number;
  total: number;
}

// Scoped to the seeded tenant — otherwise this can't tell "my backlog is
// drained" apart from unrelated activity in other tenants sharing this dev
// database.
async function fetchProgress(tenantId: string): Promise<ProgressCounts> {
  const [deliveryRows, expansionRows] = await Promise.all([
    pool.query<{ state: string; endpoint_status: string; n: string }>(
      `SELECT d.state, e.status AS endpoint_status, count(*) AS n
       FROM deliveries d
       JOIN endpoints e ON e.id = d.endpoint_id
       JOIN events ev ON ev.id = d.event_id
       WHERE ev.tenant_id = $1
       GROUP BY d.state, e.status`,
      [tenantId],
    ),
    pool.query<{ n: string }>(`SELECT count(*) AS n FROM events WHERE tenant_id = $1 AND status = 'pending_expansion'`, [tenantId]),
  ]);
  const counts: ProgressCounts = { pendingExpansion: Number(expansionRows.rows[0]!.n), claimablePending: 0, inFlight: 0, succeeded: 0, failed: 0, total: 0 };
  for (const row of deliveryRows.rows) {
    const n = Number(row.n);
    counts.total += n;
    if (row.state === "in_flight") counts.inFlight += n;
    else if (row.state === "succeeded") counts.succeeded += n;
    else if (row.state === "failed") counts.failed += n;
    else if (row.state === "pending" && row.endpoint_status === "active") counts.claimablePending += n;
  }
  return counts;
}

// Waits only for EXPANSION to finish — that's what actually creates the
// delivery rows (fast: one tenant-serialized cycle per event, ~a few ms
// each), which is what makes "100k+ deliveries" real. Draining every last
// one to a terminal state is a much slower, effectively unbounded tail
// (single-flight-per-endpoint means throughput is capped by the number of
// distinct active endpoints, not by however many worker processes exist —
// confirmed live: a full-scale run took ~29 minutes to fully drain, well
// past any reasonable background-process budget) — and isn't actually
// necessary: a dashboard meant to look like heavy traffic should show a
// visible backlog (pending/in-flight rows, real queue depth), not a queue
// that's already been fully drained by the time anyone looks at it.
async function waitForExpansionDone(tenantId: string): Promise<void> {
  const POLL_MS = 3000;
  const MAX_WAIT_MS = 5 * 60 * 1000;
  const startedAt = Date.now();
  for (;;) {
    const p = await fetchProgress(tenantId);
    log(
      `expansion progress: ${p.pendingExpansion} awaiting expansion, ${p.succeeded} succeeded, ${p.failed} failed, ` +
        `${p.inFlight} in flight, ${p.claimablePending} pending, ${p.total} total delivery rows`,
    );
    if (p.pendingExpansion === 0) return;
    if (Date.now() - startedAt > MAX_WAIT_MS) {
      log(`hit the ${MAX_WAIT_MS}ms expansion safety timeout with ${p.pendingExpansion} still unexpanded — proceeding anyway.`);
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, POLL_MS));
  }
}

// Runs delivery for a fixed window rather than until fully drained — see
// waitForExpansionDone's comment for why a partial drain is the actual
// goal here, not a shortcut around one.
async function runDeliveryWindow(tenantId: string, windowMs: number): Promise<void> {
  const POLL_MS = 5000;
  const startedAt = Date.now();
  for (;;) {
    const p = await fetchProgress(tenantId);
    log(
      `delivery progress: ${p.succeeded} succeeded, ${p.failed} failed, ${p.inFlight} in flight, ` +
        `${p.claimablePending} pending (claimable), ${p.total} total delivery rows`,
    );
    if (p.claimablePending + p.inFlight === 0) {
      log("fully drained already — no need to wait out the rest of the delivery window.");
      return;
    }
    if (Date.now() - startedAt > windowMs) {
      log(`delivery window (${windowMs}ms) elapsed with ${p.claimablePending + p.inFlight} still outstanding — that backlog is staying, by design.`);
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, POLL_MS));
  }
}

async function main(): Promise<void> {
  const tenantId = await findTenant();
  log(`seeding tenant ${tenantId}`);

  const receiver = startReceiver();
  await new Promise<void>((resolve) => receiver.listen(RECEIVER_PORT, "127.0.0.1", resolve));
  log(`local receiver listening on ${RECEIVER_BASE}`);

  const workers = Array.from({ length: WORKER_PROCESS_COUNT }, () => spawnDeliveryWorker());
  log(`spawned ${WORKER_PROCESS_COUNT} permissive delivery workers (local-receiver SSRF bypass, same pattern as make chaos/load)`);

  try {
    await insertEndpoints(tenantId);

    for (const stream of EVENT_STREAMS) {
      await insertEventStream(tenantId, stream);
    }

    const totalEvents = EVENT_STREAMS.reduce((sum, s) => sum + s.count, 0);
    log(`published ${totalEvents} events total — expansion now running for real.`);

    await waitForExpansionDone(tenantId);
    log(`expansion done — running delivery for up to ${DELIVERY_WINDOW_MS / 1000}s (not to full drain, see comment above).`);
    await runDeliveryWindow(tenantId, DELIVERY_WINDOW_MS);

    const final = await fetchProgress(tenantId);
    log(`done. final: ${final.succeeded} succeeded, ${final.failed} failed, ${final.total} total delivery rows.`);
  } finally {
    receiver.close();
    for (const w of workers) await killProcess(w);
    log("stopped local receiver and seeding workers.");
  }
}

main()
  .then(() => pool.end())
  .catch(async (err) => {
    console.error(err);
    await pool.end();
    process.exit(1);
  });
