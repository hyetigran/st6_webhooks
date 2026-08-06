import { pool } from "../src/db/pool.js";
import { hashApiKey, encryptSecret } from "../src/lib/crypto.js";
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
  opts: {
    status?: string;
    url?: string;
    signingSecret?: string;
    secondarySecret?: string;
    secondarySecretExpiresAt?: Date;
  } = {},
): Promise<{ id: string }> {
  const { rows } = await pool.query<{ id: string }>(
    `INSERT INTO endpoints (tenant_id, url, event_types, status, signing_secret, secondary_secret, secondary_secret_expires_at)
     VALUES ($1, $2, $3, $4, $5, $6, $7)
     RETURNING id`,
    [
      tenantId,
      opts.url ?? "https://example.com/hook",
      eventTypes,
      opts.status ?? "active",
      encryptSecret(opts.signingSecret ?? "whsec_test"),
      opts.secondarySecret ? encryptSecret(opts.secondarySecret) : null,
      opts.secondarySecretExpiresAt ?? null,
    ],
  );
  return { id: rows[0]!.id };
}

// Bypasses publish/expansion (ticket #17) to seed a delivery directly —
// delivery-worker and replay tests (tickets #18/#19) exercise the claim/
// send/replay paths, not expansion, so they don't need a real publish flow.
export async function createDelivery(
  tenantId: string,
  endpointId: string,
  opts: { eventType?: string; payload?: object; nextAttemptAt?: Date; state?: string; createdAt?: Date } = {},
): Promise<{ id: string; eventId: string }> {
  const { rows: eventRows } = await pool.query<{ id: string }>(
    `INSERT INTO events (tenant_id, idempotency_key, type, payload, status)
     VALUES ($1, $2, $3, $4, 'expanded')
     RETURNING id`,
    [tenantId, `delivery-fixture-${crypto.randomUUID()}`, opts.eventType ?? "order.created", JSON.stringify(opts.payload ?? { hello: "world" })],
  );
  const eventId = eventRows[0]!.id;

  // next_attempt_at/created_at default to Postgres's own now(), not a
  // Node-side `new Date()` — claim and replay queries compare these columns
  // against Postgres's now() too, and any clock skew between the Node
  // process and the Docker Postgres container (real, and enough to matter
  // here) would otherwise make "immediately claimable"/"in this window"
  // intermittently wrong.
  const { rows: deliveryRows } = await pool.query<{ id: string }>(
    `INSERT INTO deliveries (event_id, endpoint_id, state, next_attempt_at, created_at)
     VALUES ($1, $2, $3, COALESCE($4, now()), COALESCE($5, now()))
     RETURNING id`,
    [eventId, endpointId, opts.state ?? "pending", opts.nextAttemptAt ?? null, opts.createdAt ?? null],
  );
  return { id: deliveryRows[0]!.id, eventId };
}
