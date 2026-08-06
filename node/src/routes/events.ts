import { Router } from "express";
import { z } from "zod";
import { pool } from "../db/pool.js";
import { asyncHandler } from "../lib/asyncHandler.js";
import { decodeCursor, encodeCursor, parseLimit } from "../lib/pagination.js";

export const eventsRouter = Router();

const publishSchema = z.object({
  type: z.string().min(1),
  payload: z.record(z.unknown()),
});

interface EventRow {
  id: string;
  status: string;
}

interface EventListRow {
  id: string;
  type: string;
  payload: unknown;
  status: string;
  created_at: Date;
}

function serializeEvent(row: EventListRow) {
  return {
    id: row.id,
    type: row.type,
    payload: row.payload,
    status: row.status,
    created_at: row.created_at.toISOString(),
  };
}

eventsRouter.post("/events", asyncHandler(async (req, res) => {
  const parsed = publishSchema.safeParse(req.body);
  if (!parsed.success) {
    res.status(400).json({ error: { code: "invalid_request", message: parsed.error.issues[0]?.message ?? "Invalid request body" } });
    return;
  }

  const idempotencyKey = req.header("idempotency-key");
  if (!idempotencyKey) {
    res.status(400).json({ error: { code: "invalid_request", message: "Idempotency-Key header required" } });
    return;
  }

  const { type, payload } = parsed.data;

  // ON CONFLICT DO NOTHING returns zero rows on a conflict — R-6 requires
  // returning the *original* event's id, so a conflict needs a follow-up
  // SELECT rather than trusting the INSERT's RETURNING (caught in adversarial
  // review, REVIEW.md F-11; see ARCHITECTURE.md's publish sequence diagram).
  const inserted = await pool.query<EventRow>(
    `INSERT INTO events (tenant_id, idempotency_key, type, payload)
     VALUES ($1, $2, $3, $4)
     ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
     RETURNING id, status`,
    [req.tenantId, idempotencyKey, type, JSON.stringify(payload)],
  );

  let event = inserted.rows[0];
  if (!event) {
    const existing = await pool.query<EventRow>(
      `SELECT id, status FROM events WHERE tenant_id = $1 AND idempotency_key = $2`,
      [req.tenantId, idempotencyKey],
    );
    event = existing.rows[0]!;
  }

  res.status(202).json({ id: event.id, status: event.status });
}));

// R-24: searchable by id/type/endpoint/time-range. endpoint_id filters via
// EXISTS through deliveries — an event has at most one delivery per
// endpoint (one fan-out row each), so this can't return a given event twice.
eventsRouter.get("/events", asyncHandler(async (req, res) => {
  const limit = parseLimit(req.query.limit);
  const cursor = typeof req.query.before === "string" ? decodeCursor(req.query.before) : null;

  const params: unknown[] = [req.tenantId];
  const conditions: string[] = ["e.tenant_id = $1"];

  if (typeof req.query.id === "string") {
    params.push(req.query.id);
    conditions.push(`e.id = $${params.length}`);
  }
  if (typeof req.query.type === "string") {
    params.push(req.query.type);
    conditions.push(`e.type = $${params.length}`);
  }
  if (typeof req.query.from === "string") {
    params.push(req.query.from);
    conditions.push(`e.created_at >= $${params.length}`);
  }
  if (typeof req.query.to === "string") {
    params.push(req.query.to);
    conditions.push(`e.created_at <= $${params.length}`);
  }
  if (typeof req.query.endpoint_id === "string") {
    params.push(req.query.endpoint_id);
    conditions.push(`EXISTS (SELECT 1 FROM deliveries d WHERE d.event_id = e.id AND d.endpoint_id = $${params.length})`);
  }
  if (cursor) {
    params.push(cursor.createdAt, cursor.id);
    conditions.push(`(e.created_at, e.id) < ($${params.length - 1}, $${params.length})`);
  }
  params.push(limit + 1);

  const { rows } = await pool.query<EventListRow>(
    `SELECT e.id, e.type, e.payload, e.status, e.created_at
     FROM events e
     WHERE ${conditions.join(" AND ")}
     ORDER BY e.created_at DESC, e.id DESC
     LIMIT $${params.length}`,
    params,
  );

  const hasMore = rows.length > limit;
  const page = hasMore ? rows.slice(0, limit) : rows;
  const last = page[page.length - 1];

  res.json({
    events: page.map(serializeEvent),
    next_cursor: hasMore && last ? encodeCursor({ createdAt: last.created_at.toISOString(), id: last.id }) : null,
  });
}));

interface DeliveryFanoutRow {
  id: string;
  endpoint_id: string;
  state: string;
  attempt_count: number;
  next_attempt_at: Date;
}

// §7 surface 2: the event, its payload, and its fan-out across endpoints.
// Per-delivery detail (attempts, blocked_on_delivery_id) lives at its own
// GET /deliveries/{id} — this is a summary list, not the full detail.
eventsRouter.get("/events/:id", asyncHandler(async (req, res) => {
  const { rows } = await pool.query<EventListRow>(
    `SELECT id, type, payload, status, created_at FROM events WHERE id = $1 AND tenant_id = $2`,
    [req.params.id, req.tenantId],
  );
  const event = rows[0];
  if (!event) {
    res.status(404).json({ error: { code: "not_found", message: "Event not found" } });
    return;
  }

  const { rows: deliveryRows } = await pool.query<DeliveryFanoutRow>(
    `SELECT id, endpoint_id, state, attempt_count, next_attempt_at FROM deliveries WHERE event_id = $1 ORDER BY seq`,
    [event.id],
  );

  res.json({
    ...serializeEvent(event),
    deliveries: deliveryRows.map((d) => ({
      id: d.id,
      endpoint_id: d.endpoint_id,
      state: d.state,
      attempt_count: d.attempt_count,
      next_attempt_at: d.next_attempt_at.toISOString(),
    })),
  });
}));
