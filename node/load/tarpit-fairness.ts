// PRD §8 / REVIEW.md F-5 / docs/adr/0004 (R-18): "One tenant saturates the
// worker pool with slow (tarpit) receivers; a quiet tenant's added latency
// stays within roughly one outbound-timeout cycle." The stated bound: once
// any worker frees (a tarpit call completing or timing out), the fairness
// rule routes the *next* claim to whichever tenant has gone longest
// unserved — a tarpit tenant's last_served_at was just bumped when its
// workers were claimed, so a quiet tenant that's never been served (NULL
// sorts first) is served next, not another tarpit endpoint.
import { createServer } from "node:http";
import pg from "pg";
import { encryptSecret } from "../src/lib/crypto.js";
import {
  setupLoadDatabase,
  createTenant,
  spawnWorker,
  killProcess,
  waitUntil,
  assertLoad,
  runScenario,
} from "./harness.js";

const WORKER_COUNT = 3;
const OUTBOUND_TOTAL_TIMEOUT_MS = 1_000;
// ADR-0004's own safety margin for scheduling jitter.
const BOUND_MS = OUTBOUND_TOTAL_TIMEOUT_MS * 1.5 + 500;

async function createEndpoint(pool: pg.Pool, tenantId: string, url: string): Promise<string> {
  const { rows } = await pool.query<{ id: string }>(
    `INSERT INTO endpoints (tenant_id, url, event_types, signing_secret) VALUES ($1, $2, $3, $4) RETURNING id`,
    [tenantId, url, ["order.created"], encryptSecret("whsec_load")],
  );
  return rows[0]!.id;
}

async function publishDelivery(pool: pg.Pool, tenantId: string, endpointId: string): Promise<string> {
  const { rows: eventRows } = await pool.query<{ id: string }>(
    `INSERT INTO events (tenant_id, idempotency_key, type, payload, status) VALUES ($1, $2, 'order.created', '{}', 'expanded') RETURNING id`,
    [tenantId, `tarpit-fairness-${crypto.randomUUID()}`],
  );
  const { rows: deliveryRows } = await pool.query<{ id: string }>(
    `INSERT INTO deliveries (event_id, endpoint_id) VALUES ($1, $2) RETURNING id`,
    [eventRows[0]!.id, endpointId],
  );
  return deliveryRows[0]!.id;
}

await runScenario("tarpit-fairness", async () => {
  const pool = await setupLoadDatabase();

  // Never responds — every request against this receiver ties up a worker
  // for the full outbound timeout, every time.
  const tarpitServer = createServer(() => {
    /* never call res.end() */
  });
  const tarpitPort = await new Promise<number>((resolve) => {
    tarpitServer.listen(0, "127.0.0.1", () => {
      const address = tarpitServer.address();
      if (address === null || typeof address === "string") throw new Error("expected a bound TCP address");
      resolve(address.port);
    });
  });

  const quietServer = createServer((_req, res) => {
    res.writeHead(200);
    res.end();
  });
  const quietPort = await new Promise<number>((resolve) => {
    quietServer.listen(0, "127.0.0.1", () => {
      const address = quietServer.address();
      if (address === null || typeof address === "string") throw new Error("expected a bound TCP address");
      resolve(address.port);
    });
  });

  const { id: tarpitTenantId } = await createTenant(pool, "tarpit-tenant");
  const { id: quietTenantId } = await createTenant(pool, "quiet-tenant");

  // One tarpit endpoint per worker — enough to occupy the entire pool at
  // once — each with a deep backlog so a freed worker always finds more
  // tarpit work immediately ready, the worst case for the quiet tenant.
  const tarpitEndpointIds: string[] = [];
  for (let i = 0; i < WORKER_COUNT; i++) {
    const endpointId = await createEndpoint(pool, tarpitTenantId, `http://tarpit.local:${tarpitPort}/hook`);
    tarpitEndpointIds.push(endpointId);
    for (let j = 0; j < 10; j++) {
      await publishDelivery(pool, tarpitTenantId, endpointId);
    }
  }
  const quietEndpointId = await createEndpoint(pool, quietTenantId, `http://quiet.local:${quietPort}/hook`);

  const workers = Array.from({ length: WORKER_COUNT }, () =>
    spawnWorker({ env: { OUTBOUND_TOTAL_TIMEOUT_MS: String(OUTBOUND_TOTAL_TIMEOUT_MS), OUTBOUND_CONNECT_TIMEOUT_MS: "1000" } }),
  );
  try {
    // Let the pool saturate: all WORKER_COUNT tarpit endpoints busy.
    await waitUntil(
      async () => {
        const { rows } = await pool.query<{ count: string }>(
          "SELECT count(*) FROM endpoints WHERE id = ANY($1) AND busy = true",
          [tarpitEndpointIds],
        );
        return Number(rows[0]!.count) === WORKER_COUNT;
      },
      { timeoutMs: 5_000, label: "the worker pool becomes fully saturated with tarpit endpoints" },
    );

    // Now the quiet tenant publishes, into a fully saturated pool.
    const startedAt = performance.now();
    const quietDeliveryId = await publishDelivery(pool, quietTenantId, quietEndpointId);
    await waitUntil(
      async () => {
        const { rows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [quietDeliveryId]);
        return rows[0]!.state === "succeeded";
      },
      { timeoutMs: BOUND_MS + 5_000, label: "the quiet tenant's delivery eventually succeeds" },
    );
    const addedLatencyMs = performance.now() - startedAt;

    assertLoad(
      addedLatencyMs <= BOUND_MS,
      `expected the quiet tenant's added latency (${addedLatencyMs.toFixed(1)}ms) to stay within roughly one outbound-timeout cycle plus safety margin (${BOUND_MS}ms)`,
    );

    return { workerCount: WORKER_COUNT, outboundTotalTimeoutMs: OUTBOUND_TOTAL_TIMEOUT_MS, boundMs: BOUND_MS, addedLatencyMs };
  } finally {
    await Promise.all(workers.map((w) => killProcess(w)));
    tarpitServer.closeAllConnections();
    quietServer.closeAllConnections();
    await new Promise<void>((resolve) => tarpitServer.close(() => resolve()));
    await new Promise<void>((resolve) => quietServer.close(() => resolve()));
    await pool.end();
  }
});
