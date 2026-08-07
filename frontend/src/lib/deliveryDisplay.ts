import type { DeliveryState } from "../api/types";
import { formatRelativeTime } from "./format";

export function deliveryTone(state: DeliveryState): "neutral" | "accent" | "danger" {
  if (state === "failed") return "danger";
  if (state === "in_flight" || state === "succeeded") return "accent";
  return "neutral";
}

/** next_attempt_at only means something for a delivery still awaiting a
 * send — a terminal (succeeded/failed) delivery's next_attempt_at is a
 * stale leftover value, not a real scheduled time (see
 * node/src/db/migrations/001_init.sql's NOT NULL DEFAULT now()). */
export function nextAttemptDisplay(state: DeliveryState, nextAttemptAt: string): string {
  return state === "pending" || state === "in_flight" ? formatRelativeTime(nextAttemptAt) : "—";
}
