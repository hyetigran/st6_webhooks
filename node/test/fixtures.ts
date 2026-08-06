import { pool } from "../src/db/pool.js";
import { hashApiKey } from "../src/lib/crypto.js";
import { generateSecret } from "../src/lib/secrets.js";

export async function createTenant(name = "test-tenant"): Promise<{ id: string; apiKey: string }> {
  const apiKey = generateSecret("tenant");
  const { rows } = await pool.query<{ id: string }>(
    "INSERT INTO tenants (name, api_key_hash) VALUES ($1, $2) RETURNING id",
    [name, hashApiKey(apiKey)],
  );
  return { id: rows[0]!.id, apiKey };
}

export async function createEndpoint(
  tenantId: string,
  eventTypes: string[],
  opts: { status?: string } = {},
): Promise<{ id: string }> {
  // signing_secret is a plain placeholder here, not an encrypted value —
  // fine for expansion tests, which never read it. Delivery-worker tests
  // (ticket #18) that actually sign requests will need encryptSecret(...)
  // here instead.
  const { rows } = await pool.query<{ id: string }>(
    `INSERT INTO endpoints (tenant_id, url, event_types, status, signing_secret)
     VALUES ($1, 'https://example.com/hook', $2, $3, 'whsec_test')
     RETURNING id`,
    [tenantId, eventTypes, opts.status ?? "active"],
  );
  return { id: rows[0]!.id };
}
