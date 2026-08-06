// PRD §8 (R-18): "One tenant floods; a quiet tenant's p99 stays within its
// bound." Exercises the tenant-fairness claim ordering (least-recently-
// served tenant first, docs/adr/0004) under real concurrent load from a
// real worker pool, not a single worker.
import { createServer } from "node:http";
import {
  setupLoadDatabase,
  createTenant,
  spawnWorker,
  killProcess,
  percentile,
  assertLoad,
  runScenario,
} from "./harness.js";
import pg from "pg";
import { encryptSecret } from "../src/lib/crypto.js";

const WORKER_COUNT = 3;
const FLOOD_ENDPOINT_COUNT = 20;
const FLOOD_EVENTS_PER_ENDPOINT = 5;
const QUIET_EVENT_COUNT = 10;

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
    [tenantId, `noisy-neighbor-${crypto.randomUUID()}`],
  );
  const { rows: deliveryRows } = await pool.query<{ id: string }>(
    `INSERT INTO deliveries (event_id, endpoint_id) VALUES ($1, $2) RETURNING id`,
    [eventRows[0]!.id, endpointId],
  );
  return deliveryRows[0]!.id;
}

async function waitForSucceeded(pool: pg.Pool, deliveryId: string, timeoutMs: number): Promise<number> {
  const startedAt = performance.now();
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const { rows } = await pool.query<{ state: string }>("SELECT state FROM deliveries WHERE id = $1", [deliveryId]);
    if (rows[0]!.state === "succeeded") return performance.now() - startedAt;
    if (Date.now() >= deadline) throw new Error(`delivery ${deliveryId} did not succeed within ${timeoutMs}ms`);
    await new Promise((resolve) => setTimeout(resolve, 30));
  }
}

await runScenario("noisy-neighbor", async () => {
  const pool = await setupLoadDatabase();

  const server = createServer((_req, res) => {
    res.writeHead(200);
    res.end();
  });
  const port = await new Promise<number>((resolve) => {
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (address === null || typeof address === "string") throw new Error("expected a bound TCP address");
      resolve(address.port);
    });
  });
  const url = `http://load-test.local:${port}/hook`;

  const { id: floodTenantId } = await createTenant(pool, "flood-tenant");
  const { id: quietTenantId } = await createTenant(pool, "quiet-tenant");

  const floodEndpointIds: string[] = [];
  for (let i = 0; i < FLOOD_ENDPOINT_COUNT; i++) {
    floodEndpointIds.push(await createEndpoint(pool, floodTenantId, url));
  }
  const quietEndpointId = await createEndpoint(pool, quietTenantId, url);

  const workers = Array.from({ length: WORKER_COUNT }, () => spawnWorker());
  try {
    // Flood: publish a large backlog across many endpoints, all at once,
    // fire-and-forget (this is the noise — we don't wait on it).
    const floodPublishes: Promise<string>[] = [];
    for (const endpointId of floodEndpointIds) {
      for (let i = 0; i < FLOOD_EVENTS_PER_ENDPOINT; i++) {
        floodPublishes.push(publishDelivery(pool, floodTenantId, endpointId));
      }
    }
    await Promise.all(floodPublishes);

    // Quiet tenant publishes concurrently with the flood being drained —
    // measure how long each of its deliveries actually takes to complete.
    const quietLatenciesMs: number[] = [];
    for (let i = 0; i < QUIET_EVENT_COUNT; i++) {
      const deliveryId = await publishDelivery(pool, quietTenantId, quietEndpointId);
      quietLatenciesMs.push(await waitForSucceeded(pool, deliveryId, 15_000));
      await new Promise((resolve) => setTimeout(resolve, 50));
    }

    quietLatenciesMs.sort((a, b) => a - b);
    const p50 = percentile(quietLatenciesMs, 50);
    const p99 = percentile(quietLatenciesMs, 99);

    // No hard-coded absolute bound in the spec — the documented mechanism
    // (docs/adr/0004) promises the quiet tenant is never starved, not a
    // specific number. A generous, structural bound: even if every flood
    // endpoint got served once before the quiet tenant, quiet shouldn't
    // wait more than a handful of claim cycles.
    assertLoad(p99 < 5_000, `expected quiet tenant's p99 delivery latency under noisy-neighbor load to stay well bounded, got ${p99.toFixed(1)}ms`);

    const { rows: floodStats } = await pool.query<{ count: string; succeeded: string }>(
      "SELECT count(*) AS count, count(*) FILTER (WHERE state = 'succeeded') AS succeeded FROM deliveries WHERE endpoint_id = ANY($1)",
      [floodEndpointIds],
    );

    return {
      workerCount: WORKER_COUNT,
      floodDeliveryCount: Number(floodStats[0]!.count),
      floodSucceededSoFar: Number(floodStats[0]!.succeeded),
      quietP50Ms: p50,
      quietP99Ms: p99,
      quietLatenciesMs,
    };
  } finally {
    await Promise.all(workers.map((w) => killProcess(w)));
    server.closeAllConnections();
    await new Promise<void>((resolve) => server.close(() => resolve()));
    await pool.end();
  }
});
