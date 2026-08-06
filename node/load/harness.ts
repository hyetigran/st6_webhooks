// Load-specific infrastructure for `make load` scenarios (PRD §8) — real
// HTTP latency against a real spawned api/worker pool. Database bootstrap,
// condition polling, process teardown, and evidence writing are shared with
// chaos/ via ../scripts/scenarioHarness.ts; this file keeps only what's
// genuinely load-specific: spawning the API server, percentile math, and
// bulk-insert fixtures for creating thousands of endpoints/deliveries fast.
import pg from "pg";
import { spawn, type ChildProcess } from "node:child_process";
import { encryptSecret } from "../src/lib/crypto.js";
import {
  nodeDir,
  tsxNodeArgs,
  setupDatabase,
  createTenant as sharedCreateTenant,
  waitUntil,
  killProcess,
  assertScenario as assertLoad,
  makeRunScenario,
} from "../scripts/scenarioHarness.js";

const LOAD_DB_NAME = "webhooks_node_load";
export const LOAD_DATABASE_URL = `postgres://webhooks:webhooks@localhost:5532/${LOAD_DB_NAME}`;

export async function setupLoadDatabase(): Promise<pg.Pool> {
  return setupDatabase(LOAD_DB_NAME, LOAD_DATABASE_URL, { max: 20 });
}

export async function createTenant(pool: pg.Pool, name = "load-tenant"): Promise<{ id: string; apiKey: string }> {
  return sharedCreateTenant(pool, name);
}

// Bulk-inserts N endpoints in one statement — creating thousands of
// endpoints one row at a time would dominate setup time and make the
// measured publish latency meaningless by comparison.
export async function createEndpointsBulk(
  pool: pg.Pool,
  tenantId: string,
  count: number,
  opts: { url?: string; eventTypes?: string[] } = {},
): Promise<void> {
  const url = opts.url ?? "https://example.com/hook";
  const eventTypes = opts.eventTypes ?? ["order.created"];
  const encryptedSecret = encryptSecret("whsec_load");
  const values: string[] = [];
  const params: unknown[] = [];
  for (let i = 0; i < count; i++) {
    params.push(tenantId, url, eventTypes, encryptedSecret);
    const base = params.length - 4;
    values.push(`($${base + 1}, $${base + 2}, $${base + 3}, $${base + 4})`);
  }
  await pool.query(`INSERT INTO endpoints (tenant_id, url, event_types, signing_secret) VALUES ${values.join(",")}`, params);
}

// Bulk-creates N terminal (succeeded) deliveries, each with its own event —
// for replay-window-size scenarios, where "large window" means large
// history to scan, not large fan-out. One multi-row INSERT per table
// rather than N round trips.
export async function createTerminalDeliveriesBulk(
  pool: pg.Pool,
  tenantId: string,
  endpointId: string,
  count: number,
): Promise<void> {
  const eventValues: string[] = [];
  const eventParams: unknown[] = [];
  for (let i = 0; i < count; i++) {
    eventParams.push(tenantId, `load-replay-fixture-${crypto.randomUUID()}`, "order.created", "{}");
    const base = eventParams.length - 4;
    eventValues.push(`($${base + 1}, $${base + 2}, $${base + 3}, $${base + 4}, 'expanded')`);
  }
  const { rows: eventRows } = await pool.query<{ id: string }>(
    `INSERT INTO events (tenant_id, idempotency_key, type, payload, status) VALUES ${eventValues.join(",")} RETURNING id`,
    eventParams,
  );

  const deliveryValues: string[] = [];
  const deliveryParams: unknown[] = [];
  for (const event of eventRows) {
    deliveryParams.push(event.id, endpointId);
    const base = deliveryParams.length - 2;
    deliveryValues.push(`($${base + 1}, $${base + 2}, 'succeeded')`);
  }
  await pool.query(`INSERT INTO deliveries (event_id, endpoint_id, state) VALUES ${deliveryValues.join(",")}`, deliveryParams);
}

export interface SpawnProcessOptions {
  env?: Record<string, string>;
}

function baseEnv(overrides?: Record<string, string>): Record<string, string> {
  return {
    ...process.env,
    DATABASE_URL: LOAD_DATABASE_URL,
    SECRET_ENCRYPTION_KEY: "uVnfLJGuLvn8ZwxpLXFXIw8irrEhzVUIqM6SneLB6Sc=",
    ...overrides,
  } as Record<string, string>;
}

export function spawnApiServer(port: number, options: SpawnProcessOptions = {}): ChildProcess {
  return spawn(process.execPath, [...tsxNodeArgs, "src/server.ts"], {
    cwd: nodeDir,
    env: baseEnv({ PORT: String(port), ...options.env }),
    stdio: ["ignore", "pipe", "pipe"],
  });
}

// Reuses chaos/worker-entrypoint.ts — same reasoning as there: local
// receivers (needed for the tarpit-fairness scenario's controllable slow
// servers) fail the real, correct SSRF check, so load scenarios needing
// one use the same dedicated, never-shipped test entrypoint chaos/ already
// established rather than duplicating it.
export function spawnWorker(options: SpawnProcessOptions = {}): ChildProcess {
  return spawn(process.execPath, [...tsxNodeArgs, "chaos/worker-entrypoint.ts"], {
    cwd: nodeDir,
    env: baseEnv({ WORKER_IDLE_POLL_INTERVAL_MS: "20", ...options.env }),
    stdio: ["ignore", "pipe", "pipe"],
  });
}

export async function waitForServer(baseUrl: string, timeoutMs = 10_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    try {
      const res = await fetch(`${baseUrl}/healthz`);
      if (res.ok) return;
    } catch {
      // not up yet
    }
    if (Date.now() >= deadline) throw new Error(`server at ${baseUrl} did not become healthy within ${timeoutMs}ms`);
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
}

export function percentile(sortedMs: number[], p: number): number {
  if (sortedMs.length === 0) return NaN;
  const index = Math.min(sortedMs.length - 1, Math.floor((p / 100) * sortedMs.length));
  return sortedMs[index]!;
}

export const runScenario = makeRunScenario("load");
export { waitUntil, killProcess, assertLoad };
