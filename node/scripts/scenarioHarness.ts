// Shared infrastructure for chaos/ and load/ standalone scripts — the
// pieces genuinely identical between them (database bootstrap, migration
// runner, tenant fixture, condition polling, process teardown, evidence
// writing). Each of chaos/harness.ts and load/harness.ts keeps only what's
// actually different: chaos's aggressive lease/backoff env overrides and
// worker-entrypoint spawning vs. load's api-server spawning, percentile
// math, and bulk-insert helpers.
import pg from "pg";
import type { ChildProcess } from "node:child_process";
import { readdir, readFile, mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { hashApiKey } from "../src/lib/crypto.js";
import { generateSecret } from "../src/lib/secrets.js";

export const nodeDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const repoRoot = path.resolve(nodeDir, "..");
export const tsxPreflight = path.join(nodeDir, "node_modules", "tsx", "dist", "preflight.cjs");
export const tsxLoader = path.join(nodeDir, "node_modules", "tsx", "dist", "loader.mjs");
export const tsxNodeArgs = ["--require", tsxPreflight, "--import", `file://${tsxLoader}`];

const ADMIN_URL = "postgres://webhooks:webhooks@localhost:5532/postgres";

// encryptSecret()/config.ts read this lazily (not at import time) — safe to
// default here for standalone tooling, same value every test tier uses.
process.env.SECRET_ENCRYPTION_KEY ??= "uVnfLJGuLvn8ZwxpLXFXIw8irrEhzVUIqM6SneLB6Sc=";

async function ensureDatabase(dbName: string): Promise<void> {
  const admin = new pg.Pool({ connectionString: ADMIN_URL });
  try {
    const { rows } = await admin.query("SELECT 1 FROM pg_database WHERE datname = $1", [dbName]);
    if (rows.length === 0) {
      await admin.query(`CREATE DATABASE ${dbName}`);
    }
  } finally {
    await admin.end();
  }
}

async function applyMigrations(pool: pg.Pool): Promise<void> {
  await pool.query(
    `CREATE TABLE IF NOT EXISTS schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
  );
  const migrationsDir = path.join(nodeDir, "src/db/migrations");
  const files = (await readdir(migrationsDir)).filter((f) => f.endsWith(".sql")).sort();
  const { rows: appliedRows } = await pool.query<{ filename: string }>("SELECT filename FROM schema_migrations");
  const applied = new Set(appliedRows.map((r) => r.filename));
  for (const file of files) {
    if (applied.has(file)) continue;
    const sql = await readFile(path.join(migrationsDir, file), "utf8");
    const client = await pool.connect();
    try {
      await client.query("BEGIN");
      await client.query(sql);
      await client.query("INSERT INTO schema_migrations (filename) VALUES ($1)", [file]);
      await client.query("COMMIT");
    } catch (err) {
      await client.query("ROLLBACK");
      throw err;
    } finally {
      client.release();
    }
  }
}

// A dedicated database per script category — separate from local dev data,
// the vitest test DB, and each other — so chaos/load runs never race
// concurrent `npm test` or `npm run dev` activity.
export async function setupDatabase(dbName: string, connectionString: string, poolOptions: pg.PoolConfig = {}): Promise<pg.Pool> {
  await ensureDatabase(dbName);
  const pool = new pg.Pool({ connectionString, ...poolOptions });
  await applyMigrations(pool);
  await pool.query("TRUNCATE tenants, endpoints, events, deliveries, attempts, replays RESTART IDENTITY CASCADE");
  return pool;
}

export async function createTenant(pool: pg.Pool, name: string): Promise<{ id: string; apiKey: string }> {
  const apiKey = generateSecret("tenant");
  const { rows } = await pool.query<{ id: string }>("INSERT INTO tenants (name, api_key_hash) VALUES ($1, $2) RETURNING id", [
    name,
    hashApiKey(apiKey),
  ]);
  return { id: rows[0]!.id, apiKey };
}

export async function waitUntil(
  check: () => Promise<boolean>,
  opts: { timeoutMs: number; intervalMs?: number; label: string },
): Promise<void> {
  const interval = opts.intervalMs ?? 50;
  const deadline = Date.now() + opts.timeoutMs;
  for (;;) {
    if (await check()) return;
    if (Date.now() >= deadline) throw new Error(`waitUntil timed out after ${opts.timeoutMs}ms: ${opts.label}`);
    await new Promise((resolve) => setTimeout(resolve, interval));
  }
}

export async function killProcess(child: ChildProcess, signal: NodeJS.Signals = "SIGKILL"): Promise<void> {
  // 'exit' only ever fires once — awaiting it again on an already-exited
  // child (a scenario's own kill followed by a cleanup-time kill in
  // `finally`) would hang forever, since the event already happened.
  if (child.exitCode !== null || child.signalCode !== null) return;
  child.kill(signal);
  if (signal === "SIGKILL" || signal === "SIGTERM") {
    await new Promise<void>((resolve) => child.once("exit", () => resolve()));
  }
}

export class ScenarioAssertionError extends Error {}

export function assertScenario(condition: boolean, message: string): asserts condition {
  if (!condition) throw new ScenarioAssertionError(message);
}

async function writeEvidence(category: "chaos" | "load", scenario: string, data: Record<string, unknown>): Promise<void> {
  const evidenceDir = path.join(repoRoot, "evidence", category);
  await mkdir(evidenceDir, { recursive: true });
  await writeFile(
    path.join(evidenceDir, `${scenario}.json`),
    JSON.stringify({ scenario, timestamp: new Date().toISOString(), ...data }, null, 2),
  );
}

export function makeRunScenario(category: "chaos" | "load") {
  return async function runScenario(name: string, fn: () => Promise<Record<string, unknown>>): Promise<void> {
    const startedAt = Date.now();
    try {
      const result = await fn();
      const durationMs = Date.now() - startedAt;
      console.log(`[PASS] ${name} (${durationMs}ms)`);
      if (category === "load") console.log(JSON.stringify(result, null, 2));
      await writeEvidence(category, name, { status: "pass", durationMs, ...result });
      process.exitCode = 0;
    } catch (err) {
      const durationMs = Date.now() - startedAt;
      const message = err instanceof Error ? err.message : String(err);
      console.error(`[FAIL] ${name} (${durationMs}ms): ${message}`);
      await writeEvidence(category, name, { status: "fail", durationMs, error: message });
      process.exitCode = 1;
    }
  };
}
