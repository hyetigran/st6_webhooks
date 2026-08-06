// R-12/R-23, CONTEXT.md's "Blocked" definition: a pending delivery that
// isn't its endpoint's current head (the oldest still-unresolved delivery,
// by seq — docs/adr/0007) hasn't been attempted yet because something ahead
// of it in the queue hasn't cleared. in_flight/succeeded/failed are never
// blocked — only a queued, not-yet-reached pending delivery can be. Shared
// between GET /deliveries/:id and GET /endpoints/:id/deliveries, the two
// routes that need this computation.
export const HEAD_DELIVERY_SELECT = `
  (SELECT h.id FROM deliveries h
   WHERE h.endpoint_id = d.endpoint_id AND h.state IN ('pending', 'in_flight')
   ORDER BY h.seq LIMIT 1) AS head_delivery_id
`;

export function computeBlockedOnDeliveryId(delivery: { id: string; state: string; head_delivery_id: string | null }): string | null {
  return delivery.state === "pending" && delivery.head_delivery_id !== delivery.id ? delivery.head_delivery_id : null;
}

export interface DeliverySummaryRow {
  id: string;
  event_id: string;
  state: string;
  attempt_count: number;
  next_attempt_at: Date;
  head_delivery_id: string | null;
}

// The fields GET /deliveries/:id and GET /endpoints/:id/deliveries both
// expose per delivery — deliveries.ts adds endpoint_id/last_response/
// attempts on top for its single-delivery detail view.
export function serializeDeliverySummary(row: DeliverySummaryRow) {
  return {
    id: row.id,
    event_id: row.event_id,
    state: row.state,
    attempt_count: row.attempt_count,
    next_attempt_at: row.next_attempt_at.toISOString(),
    blocked_on_delivery_id: computeBlockedOnDeliveryId(row),
  };
}
