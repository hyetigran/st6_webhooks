import { Router } from "express";
import { z } from "zod";
import { pool } from "../db/pool.js";
import { asyncHandler } from "../lib/asyncHandler.js";

export const replaysRouter = Router();

const replaySchema = z
  .object({
    range_start: z.string().datetime(),
    range_end: z.string().datetime(),
  })
  .refine((body) => new Date(body.range_end) >= new Date(body.range_start), {
    message: "range_end must not be before range_start",
  });

interface ReplayRow {
  id: string;
  status: string;
}

replaysRouter.post(
  "/endpoints/:id/replays",
  asyncHandler(async (req, res) => {
    const parsed = replaySchema.safeParse(req.body);
    if (!parsed.success) {
      res.status(400).json({ error: { code: "invalid_request", message: parsed.error.issues[0]?.message ?? "Invalid request body" } });
      return;
    }

    const idempotencyKey = req.header("idempotency-key");
    if (!idempotencyKey) {
      res.status(400).json({ error: { code: "invalid_request", message: "Idempotency-Key header required" } });
      return;
    }

    const { rows: endpointRows } = await pool.query(`SELECT id FROM endpoints WHERE id = $1 AND tenant_id = $2`, [
      req.params.id,
      req.tenantId,
    ]);
    if (!endpointRows[0]) {
      res.status(404).json({ error: { code: "not_found", message: "Endpoint not found" } });
      return;
    }

    const { range_start, range_end } = parsed.data;
    const inserted = await pool.query<ReplayRow>(
      `INSERT INTO replays (endpoint_id, idempotency_key, range_start, range_end)
       VALUES ($1, $2, $3, $4)
       ON CONFLICT (endpoint_id, idempotency_key) DO NOTHING
       RETURNING id, status`,
      [req.params.id, idempotencyKey, range_start, range_end],
    );
    let replay = inserted.rows[0];
    if (!replay) {
      const existing = await pool.query<ReplayRow>(`SELECT id, status FROM replays WHERE endpoint_id = $1 AND idempotency_key = $2`, [
        req.params.id,
        idempotencyKey,
      ]);
      replay = existing.rows[0]!;
    }

    res.status(202).json({ id: replay.id, status: replay.status });
  }),
);
