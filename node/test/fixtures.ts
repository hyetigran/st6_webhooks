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

// Bypasses publish/expansion (ticket #17) to seed a delivery directly in
// 'pending' state — delivery-worker tests (ticket #18) exercise the claim
// and send path, not expansion, so they don't need a real publish flow.
export async function createPendingDelivery(
  tenantId: string,
  endpointId: string,
  opts: { eventType?: string; payload?: object; nextAttemptAt?: Date } = {},
): Promise<{ id: string; eventId: string }> {
  const { rows: eventRows } = await pool.query<{ id: string }>(
    `INSERT INTO events (tenant_id, idempotency_key, type, payload, status)
     VALUES ($1, $2, $3, $4, 'expanded')
     RETURNING id`,
    [tenantId, `delivery-fixture-${crypto.randomUUID()}`, opts.eventType ?? "order.created", JSON.stringify(opts.payload ?? { hello: "world" })],
  );
  const eventId = eventRows[0]!.id;

  // next_attempt_at defaults to Postgres's own now(), not a Node-side
  // `new Date()` — claimDelivery compares this column against Postgres's
  // now() too, and any clock skew between the Node process and the Docker
  // Postgres container (real, and enough to matter here) would otherwise
  // make "immediately claimable" intermittently false.
  const { rows: deliveryRows } = await pool.query<{ id: string }>(
    opts.nextAttemptAt
      ? `INSERT INTO deliveries (event_id, endpoint_id, next_attempt_at) VALUES ($1, $2, $3) RETURNING id`
      : `INSERT INTO deliveries (event_id, endpoint_id, next_attempt_at) VALUES ($1, $2, now()) RETURNING id`,
    opts.nextAttemptAt ? [eventId, endpointId, opts.nextAttemptAt] : [eventId, endpointId],
  );
  return { id: deliveryRows[0]!.id, eventId };
}
