import { Router } from "express";
import { z } from "zod";
import { pool } from "../db/pool.js";
import { asyncHandler } from "../lib/asyncHandler.js";

export const eventsRouter = Router();

const publishSchema = z.object({
  type: z.string().min(1),
  payload: z.record(z.unknown()),
});

interface EventRow {
  id: string;
  status: string;
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
