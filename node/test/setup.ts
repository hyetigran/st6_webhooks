// Runs before each test file. Truncates all tables so every test starts
// from a clean slate — simpler than per-test transactions given the app
// code uses a module-level singleton pool, not an injected client.

import { beforeEach } from "vitest";
import { pool } from "../src/db/pool.js";

beforeEach(async () => {
  await pool.query(
    "TRUNCATE tenants, endpoints, events, deliveries, attempts, replays RESTART IDENTITY CASCADE",
  );
});
