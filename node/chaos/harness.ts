// Chaos-specific infrastructure for `make chaos` scenarios (PRD §8) — real
// process kill/SIGSTOP/SIGCONT against a real spawned worker pool. Database
// bootstrap, condition polling, process teardown, and evidence writing are
// shared with load/ via ../scripts/scenarioHarness.ts; this file keeps only
// what's genuinely chaos-specific: the worker-entrypoint spawn (with
// aggressive lease/backoff env overrides so scenarios finish in seconds)
// and chaos-flavored fixtures.
import { spawn, type ChildProcess } from "node:child_process";
import { hashApiKey, encryptSecret } from "../src/lib/crypto.js";
import pg from "pg";
import {
  nodeDir,
  tsxNodeArgs,
  setupDatabase,
  createTenant as sharedCreateTenant,
  waitUntil,
  killProcess as killWorker,
  assertScenario as assertChaos,
  makeRunScenario,
} from "../scripts/scenarioHarness.js";

const CHAOS_DB_NAME = "webhooks_node_chaos";
export const CHAOS_DATABASE_URL = `postgres://webhooks:webhooks@localhost:5532/${CHAOS_DB_NAME}`;

export async function setupChaosDatabase(): Promise<pg.Pool> {
  return setupDatabase(CHAOS_DB_NAME, CHAOS_DATABASE_URL);
}

export async function createTenant(pool: pg.Pool, name = "chaos-tenant"): Promise<{ id: string; apiKey: string }> {
  return sharedCreateTenant(pool, name);
}

export async function createEndpoint(
  pool: pg.Pool,
  tenantId: string,
  eventTypes: string[],
  opts: { url?: string; signingSecret?: string } = {},
): Promise<{ id: string }> {
  const { rows } = await pool.query<{ id: string }>(
    `INSERT INTO endpoints (tenant_id, url, event_types, signing_secret) VALUES ($1, $2, $3, $4) RETURNING id`,
    [tenantId, opts.url ?? "https://example.com/hook", eventTypes, encryptSecret(opts.signingSecret ?? "whsec_chaos")],
  );
  return { id: rows[0]!.id };
}

export async function createPendingDelivery(
  pool: pg.Pool,
  tenantId: string,
  endpointId: string,
  opts: { eventType?: string; payload?: object } = {},
): Promise<{ id: string; eventId: string }> {
  const { rows: eventRows } = await pool.query<{ id: string }>(
    `INSERT INTO events (tenant_id, idempotency_key, type, payload, status) VALUES ($1, $2, $3, $4, 'expanded') RETURNING id`,
    [tenantId, `chaos-fixture-${crypto.randomUUID()}`, opts.eventType ?? "order.created", JSON.stringify(opts.payload ?? { hello: "chaos" })],
  );
  const eventId = eventRows[0]!.id;
  const { rows: deliveryRows } = await pool.query<{ id: string }>(
    `INSERT INTO deliveries (event_id, endpoint_id, next_attempt_at) VALUES ($1, $2, now()) RETURNING id`,
    [eventId, endpointId],
  );
  return { id: deliveryRows[0]!.id, eventId };
}

export interface SpawnWorkerOptions {
  env?: Record<string, string>;
}

// Small timeouts/poll-interval by default — PRD §8's "time is injected":
// chaos scenarios need lease expiry and backoff to happen in seconds, not
// the production defaults' tens of seconds.
//
// Spawns node directly with tsx's loader flags rather than tsx's own CLI
// binary: that CLI re-execs into a *second*, inner node process carrying
// these same flags (Node's loader-hook API only applies --import at process
// start). spawn() would only get a handle to the outer wrapper — and
// SIGKILL is uncatchable, so a killed wrapper can't relay it to its child,
// leaving the real worker process orphaned and un-killable.
export function spawnWorker(options: SpawnWorkerOptions = {}): ChildProcess {
  return spawn(process.execPath, [...tsxNodeArgs, "chaos/worker-entrypoint.ts"], {
    cwd: nodeDir,
    env: {
      ...process.env,
      DATABASE_URL: CHAOS_DATABASE_URL,
      SECRET_ENCRYPTION_KEY: "uVnfLJGuLvn8ZwxpLXFXIw8irrEhzVUIqM6SneLB6Sc=",
      WORKER_IDLE_POLL_INTERVAL_MS: "50",
      OUTBOUND_CONNECT_TIMEOUT_MS: "1000",
      OUTBOUND_TOTAL_TIMEOUT_MS: "1000",
      LEASE_MIN_DURATION_MS: "1000",
      BACKOFF_BASE_DELAY_MS: "50",
      BACKOFF_MAX_DELAY_MS: "200",
      ...options.env,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
}

export const runScenario = makeRunScenario("chaos");
export { waitUntil, killWorker, assertChaos };
