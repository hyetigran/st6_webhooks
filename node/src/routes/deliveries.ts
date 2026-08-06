import { Router } from "express";
import { pool } from "../db/pool.js";
import { asyncHandler } from "../lib/asyncHandler.js";
import { HEAD_DELIVERY_SELECT, serializeDeliverySummary } from "../lib/deliveryQueries.js";

export const deliveriesRouter = Router();

interface DeliveryDetailRow {
  id: string;
  event_id: string;
  endpoint_id: string;
  state: string;
  attempt_count: number;
  next_attempt_at: Date;
  head_delivery_id: string | null;
}

// Route table (Shared REST API contract ticket): "no pagination needed,
// capped at 6 attempts" — an explicit contract cap, independent of
// config.backoff.maxAttempts (also 6 by default, but the two aren't the
// same promise; this route must cap regardless of what that config is set
// to).
const MAX_ATTEMPTS_IN_RESPONSE = 6;

interface AttemptRow {
  attempt_number: number;
  sent_at: Date | null;
  response_status: number | null;
  response_body_truncated: string | null;
  duration_ms: number | null;
  error_class: string | null;
}

function serializeAttempt(row: AttemptRow) {
  return {
    attempt_number: row.attempt_number,
    sent_at: row.sent_at ? row.sent_at.toISOString() : null,
    response_status: row.response_status,
    response_body_truncated: row.response_body_truncated,
    duration_ms: row.duration_ms,
    error_class: row.error_class,
  };
}

deliveriesRouter.get(
  "/deliveries/:id",
  asyncHandler(async (req, res) => {
    const { rows } = await pool.query<DeliveryDetailRow>(
      `SELECT d.id, d.event_id, d.endpoint_id, d.state, d.attempt_count, d.next_attempt_at, ${HEAD_DELIVERY_SELECT}
       FROM deliveries d
       JOIN endpoints e ON e.id = d.endpoint_id
       WHERE d.id = $1 AND e.tenant_id = $2`,
      [req.params.id, req.tenantId],
    );
    const delivery = rows[0];
    if (!delivery) {
      res.status(404).json({ error: { code: "not_found", message: "Delivery not found" } });
      return;
    }

    // Fetch newest-first so the cap keeps the most recent attempts (not the
    // first 6, in case config ever allows more attempts than the response
    // cap), then reverse for a chronological-order response.
    const { rows: latestFirstRows } = await pool.query<AttemptRow>(
      `SELECT attempt_number, sent_at, response_status, response_body_truncated, duration_ms, error_class
       FROM attempts WHERE delivery_id = $1 ORDER BY attempt_number DESC LIMIT $2`,
      [delivery.id, MAX_ATTEMPTS_IN_RESPONSE],
    );
    const lastAttempt = latestFirstRows[0];
    const attemptRows = [...latestFirstRows].reverse();

    res.json({
      ...serializeDeliverySummary(delivery),
      endpoint_id: delivery.endpoint_id,
      last_response: lastAttempt
        ? {
            response_status: lastAttempt.response_status,
            response_body_truncated: lastAttempt.response_body_truncated,
            duration_ms: lastAttempt.duration_ms,
            error_class: lastAttempt.error_class,
          }
        : null,
      attempts: attemptRows.map(serializeAttempt),
    });
  }),
);
