// Runs once before the whole test run (vitest's globalSetup). Creates the
// test database if it doesn't exist, then applies migrations against it —
// same migrations the dev/prod DB uses, so tests run against the real
// schema, not a hand-maintained copy of it.

import pg from "pg";
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

const TEST_DB_NAME = "webhooks_node_test";
const ADMIN_URL = "postgres://webhooks:webhooks@localhost:5532/postgres";
export const TEST_DATABASE_URL = `postgres://webhooks:webhooks@localhost:5532/${TEST_DB_NAME}`;

async function ensureTestDatabase(): Promise<void> {
  const admin = new pg.Pool({ connectionString: ADMIN_URL });
  try {
    const { rows } = await admin.query("SELECT 1 FROM pg_database WHERE datname = $1", [TEST_DB_NAME]);
    if (rows.length === 0) {
      // CREATE DATABASE can't run inside a transaction/parameterized query.
      await admin.query(`CREATE DATABASE ${TEST_DB_NAME}`);
    }
  } finally {
    await admin.end();
  }
}

async function applyMigrations(): Promise<void> {
  const pool = new pg.Pool({ connectionString: TEST_DATABASE_URL });
  try {
    await pool.query(`
      CREATE TABLE IF NOT EXISTS schema_migrations (
        filename    TEXT PRIMARY KEY,
        applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
      )
    `);

    const migrationsDir = path.resolve(import.meta.dirname, "../src/db/migrations");
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
  } finally {
    await pool.end();
  }
}

export default async function globalSetup(): Promise<void> {
  await ensureTestDatabase();
  await applyMigrations();
}
