import { Router } from "express";
import { z } from "zod";
import { pool } from "../db/pool.js";
import { validateEndpointUrl } from "../validation/url.js";
import { generateSecret } from "../lib/secrets.js";
import { encryptSecret } from "../lib/crypto.js";
import { decodeCursor, encodeCursor, decodeSeqCursor, encodeSeqCursor, parseLimit } from "../lib/pagination.js";
import { HEAD_DELIVERY_SELECT, serializeDeliverySummary } from "../lib/deliveryQueries.js";
import { secretRotation } from "../config.js";
import { asyncHandler } from "../lib/asyncHandler.js";

export const endpointsRouter = Router();

const registerSchema = z.object({
  url: z.string().min(1),
  event_types: z.array(z.string().min(1)).min(1),
});

interface EndpointRow {
  id: string;
  url: string;
  event_types: string[];
  status: string;
  signing_secret: string;
  created_at: Date;
}

interface EndpointHealthRow extends EndpointRow {
  queue_depth: string;
  oldest_pending_at: Date | null;
  recent_success_rate: string | null;
}

function serializeEndpoint(row: EndpointRow) {
  return {
    id: row.id,
    url: row.url,
    event_types: row.event_types,
    status: row.status,
    created_at: row.created_at.toISOString(),
  };
}

function serializeEndpointWithHealth(row: EndpointHealthRow) {
  return {
    ...serializeEndpoint(row),
    queue_depth: Number(row.queue_depth),
    oldest_pending_at: row.oldest_pending_at ? row.oldest_pending_at.toISOString() : null,
    recent_success_rate: row.recent_success_rate === null ? null : Number(row.recent_success_rate),
  };
}

// Health subquery shared by GET /endpoints and GET /endpoints/:id (R-25):
// queue depth + oldest pending delivery + recent success rate over the last
// 50 terminal deliveries for that endpoint.
const HEALTH_SELECT = `
  e.*,
  (SELECT count(*) FROM deliveries d WHERE d.endpoint_id = e.id AND d.state IN ('pending', 'in_flight')) AS queue_depth,
  (SELECT min(d.created_at) FROM deliveries d WHERE d.endpoint_id = e.id AND d.state IN ('pending', 'in_flight')) AS oldest_pending_at,
  (
    SELECT avg((d.state = 'succeeded')::int)::numeric
    FROM (
      SELECT state FROM deliveries d2
      WHERE d2.endpoint_id = e.id AND d2.state IN ('succeeded', 'failed')
      ORDER BY d2.created_at DESC
      LIMIT 50
    ) d
  ) AS recent_success_rate
`;

endpointsRouter.post("/endpoints", asyncHandler(async (req, res) => {
  const parsed = registerSchema.safeParse(req.body);
  if (!parsed.success) {
    res.status(400).json({ error: { code: "invalid_request", message: parsed.error.issues[0]?.message ?? "Invalid request body" } });
    return;
  }

  const { url, event_types } = parsed.data;
  const validation = await validateEndpointUrl(url);
  if (!validation.allowed) {
    res.status(400).json({ error: { code: "url_not_allowed", message: validation.reason ?? "URL not allowed" } });
    return;
  }

  const signingSecret = generateSecret("whsec");
  const { rows } = await pool.query<EndpointRow>(
    `INSERT INTO endpoints (tenant_id, url, event_types, signing_secret)
     VALUES ($1, $2, $3, $4)
     RETURNING id, url, event_types, status, signing_secret, created_at`,
    [req.tenantId, url, event_types, encryptSecret(signingSecret)],
  );
  const endpoint = rows[0]!;

  // signing_secret is viewable once (R-3) — the plaintext generated above,
  // not endpoint.signing_secret (which is the encrypted value now stored).
  res.status(201).json({ ...serializeEndpoint(endpoint), signing_secret: signingSecret });
}));

endpointsRouter.get("/endpoints", asyncHandler(async (req, res) => {
  const limit = parseLimit(req.query.limit);
  const cursor = typeof req.query.before === "string" ? decodeCursor(req.query.before) : null;

  const params: unknown[] = [req.tenantId];
  let cursorClause = "";
  if (cursor) {
    params.push(cursor.createdAt, cursor.id);
    cursorClause = `AND (e.created_at, e.id) < ($${params.length - 1}, $${params.length})`;
  }
  params.push(limit + 1);

  const { rows } = await pool.query<EndpointHealthRow>(
    `SELECT ${HEALTH_SELECT}
     FROM endpoints e
     WHERE e.tenant_id = $1 ${cursorClause}
     ORDER BY e.created_at DESC, e.id DESC
     LIMIT $${params.length}`,
    params,
  );

  const hasMore = rows.length > limit;
  const page = hasMore ? rows.slice(0, limit) : rows;
  const last = page[page.length - 1];

  res.json({
    endpoints: page.map(serializeEndpointWithHealth),
    next_cursor: hasMore && last ? encodeCursor({ createdAt: last.created_at.toISOString(), id: last.id }) : null,
  });
}));

endpointsRouter.get("/endpoints/:id", asyncHandler(async (req, res) => {
  const { rows } = await pool.query<EndpointHealthRow>(
    `SELECT ${HEALTH_SELECT} FROM endpoints e WHERE e.id = $1 AND e.tenant_id = $2`,
    [req.params.id, req.tenantId],
  );
  const endpoint = rows[0];
  if (!endpoint) {
    res.status(404).json({ error: { code: "not_found", message: "Endpoint not found" } });
    return;
  }
  res.json(serializeEndpointWithHealth(endpoint));
}));

endpointsRouter.post("/endpoints/:id/pause", asyncHandler(async (req, res) => {
  const { rows } = await pool.query<EndpointRow>(
    `UPDATE endpoints SET status = 'paused'
     WHERE id = $1 AND tenant_id = $2
     RETURNING id, url, event_types, status, signing_secret, created_at`,
    [req.params.id, req.tenantId],
  );
  const endpoint = rows[0];
  if (!endpoint) {
    res.status(404).json({ error: { code: "not_found", message: "Endpoint not found" } });
    return;
  }
  res.json(serializeEndpoint(endpoint));
}));

endpointsRouter.post("/endpoints/:id/resume", asyncHandler(async (req, res) => {
  const { rows } = await pool.query<EndpointRow>(
    `UPDATE endpoints SET status = 'active'
     WHERE id = $1 AND tenant_id = $2
     RETURNING id, url, event_types, status, signing_secret, created_at`,
    [req.params.id, req.tenantId],
  );
  const endpoint = rows[0];
  if (!endpoint) {
    res.status(404).json({ error: { code: "not_found", message: "Endpoint not found" } });
    return;
  }

  // R-14: resuming is an explicit operator action that states the ordering
  // consequence. Two parts to that consequence, checked regardless of what
  // status this endpoint was resuming from (halted or merely paused) — a
  // single code path means there's no way to reach "active" without both
  // being surfaced (REVIEW.md F-12: an earlier design let a halted -> paused
  // -> active path skip this).
  const { rows: countRows } = await pool.query<{ count: string }>(
    `SELECT count(*) FROM deliveries WHERE endpoint_id = $1 AND state IN ('pending', 'in_flight')`,
    [endpoint.id],
  );

  // A 'failed' delivery is terminal — the claim query never reconsiders it.
  // Resuming does not retry it; only an explicit replay would, and a replay
  // appends at the tail (out of original order), not in place. Anything
  // failed *older than* the oldest still-pending delivery is being left
  // behind by this resume action, not just historical noise.
  const { rows: skippedRows } = await pool.query<{ id: string }>(
    `SELECT id FROM deliveries
     WHERE endpoint_id = $1 AND state = 'failed'
       AND created_at < COALESCE(
         (SELECT MIN(created_at) FROM deliveries WHERE endpoint_id = $1 AND state IN ('pending', 'in_flight')),
         'infinity'
       )
     ORDER BY created_at`,
    [endpoint.id],
  );

  res.json({
    ...serializeEndpoint(endpoint),
    pending_delivery_count: Number(countRows[0]!.count),
    skipped_failed_delivery_ids: skippedRows.map((r) => r.id),
  });
}));

endpointsRouter.post("/endpoints/:id/secret/rotate", asyncHandler(async (req, res) => {
  const newSecret = generateSecret("whsec");
  const overlapExpiresAt = new Date(Date.now() + secretRotation.overlapHours * 60 * 60 * 1000);

  // ADR-0003: the current secret moves to secondary_secret and stays active
  // (the sender signs with both) until overlap_expires_at — not a
  // receiver-only dual-check, which can't work before the receiver has the
  // new secret in hand.
  const { rows } = await pool.query<{ id: string }>(
    `UPDATE endpoints
     SET secondary_secret = signing_secret,
         secondary_secret_expires_at = $1,
         signing_secret = $2
     WHERE id = $3 AND tenant_id = $4
     RETURNING id`,
    // secondary_secret copies whatever's already in signing_secret (already
    // encrypted, moved as-is); the new value is encrypted before storing.
    [overlapExpiresAt, encryptSecret(newSecret), req.params.id, req.tenantId],
  );
  if (!rows[0]) {
    res.status(404).json({ error: { code: "not_found", message: "Endpoint not found" } });
    return;
  }

  res.json({
    signing_secret: newSecret,
    overlap_expires_at: overlapExpiresAt.toISOString(),
  });
}));

interface QueueDeliveryRow {
  id: string;
  event_id: string;
  state: string;
  attempt_count: number;
  next_attempt_at: Date;
  seq: string;
  head_delivery_id: string | null;
}

// §7 surface 4: ordered deliveries for one endpoint, head highlighted.
// Ascending by seq (head first, the actionable item) — deliberately the
// opposite direction from every other list route's newest-first paging, so
// this uses its own `after`/seq-based cursor (docs/adr/0007) rather than
// the shared `before`/created_at+id one: reusing created_at here would hit
// the exact same same-endpoint tie risk that column was added to avoid.
endpointsRouter.get("/endpoints/:id/deliveries", asyncHandler(async (req, res) => {
  const { rows: endpointRows } = await pool.query("SELECT id FROM endpoints WHERE id = $1 AND tenant_id = $2", [
    req.params.id,
    req.tenantId,
  ]);
  if (!endpointRows[0]) {
    res.status(404).json({ error: { code: "not_found", message: "Endpoint not found" } });
    return;
  }

  const limit = parseLimit(req.query.limit);
  const cursor = typeof req.query.after === "string" ? decodeSeqCursor(req.query.after) : null;

  const params: unknown[] = [req.params.id];
  let cursorClause = "";
  if (cursor) {
    params.push(cursor.seq);
    cursorClause = `AND d.seq > $${params.length}`;
  }
  params.push(limit + 1);

  const { rows } = await pool.query<QueueDeliveryRow>(
    `SELECT d.id, d.event_id, d.state, d.attempt_count, d.next_attempt_at, d.seq, ${HEAD_DELIVERY_SELECT}
     FROM deliveries d
     WHERE d.endpoint_id = $1 ${cursorClause}
     ORDER BY d.seq ASC
     LIMIT $${params.length}`,
    params,
  );

  const hasMore = rows.length > limit;
  const page = hasMore ? rows.slice(0, limit) : rows;
  const last = page[page.length - 1];

  res.json({
    deliveries: page.map(serializeDeliverySummary),
    next_cursor: hasMore && last ? encodeSeqCursor({ seq: Number(last.seq) }) : null,
  });
}));
