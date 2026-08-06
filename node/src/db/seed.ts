// Tenants are seeded out-of-band, not self-service (Shared REST API contract
// ticket — no signup flow is in scope). This script creates a demo tenant
// with a printed API key for local development and the test suite.

import { generateSecret } from "../lib/secrets.js";
import { hashApiKey } from "../lib/crypto.js";
import { pool } from "./pool.js";

async function seed(): Promise<void> {
  const name = process.argv[2] ?? "demo";
  const apiKey = generateSecret("tenant");

  // Only the hash is stored (REVIEW.md F-16) — this is the one and only
  // place the plaintext key is ever printed.
  const { rows } = await pool.query<{ id: string }>(
    `INSERT INTO tenants (name, api_key_hash) VALUES ($1, $2) RETURNING id`,
    [name, hashApiKey(apiKey)],
  );

  console.log(`Created tenant "${name}" (${rows[0]!.id})`);
  console.log(`API key: ${apiKey}`);
  await pool.end();
}

seed().catch((err) => {
  console.error(err);
  process.exit(1);
});
