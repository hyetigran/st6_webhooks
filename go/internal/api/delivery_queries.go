package api

import "time"

// headDeliverySelect mirrors node/src/lib/deliveryQueries.ts's
// HEAD_DELIVERY_SELECT. R-12/R-23, CONTEXT.md's "Blocked" definition: a
// pending delivery that isn't its endpoint's current head (the oldest
// still-unresolved delivery, by seq — docs/adr/0007) hasn't been attempted
// yet because something ahead of it in the queue hasn't cleared. in_flight/
// succeeded/failed are never blocked — only a queued, not-yet-reached
// pending delivery can be. Shared between GET /deliveries/:id and
// GET /endpoints/:id/deliveries, the two routes that need this computation.
const headDeliverySelect = `
	(SELECT h.id FROM deliveries h
	 WHERE h.endpoint_id = d.endpoint_id AND h.state IN ('pending', 'in_flight')
	 ORDER BY h.seq LIMIT 1) AS head_delivery_id
`

// deliverySummaryRow is the fields GET /deliveries/:id and
// GET /endpoints/:id/deliveries both expose per delivery — deliveries.go
// adds endpoint_id/last_response/attempts on top for its single-delivery
// detail view.
type deliverySummaryRow struct {
	ID             string
	EventID        string
	State          string
	AttemptCount   int
	NextAttemptAt  time.Time
	HeadDeliveryID *string
}

func computeBlockedOnDeliveryID(row deliverySummaryRow) *string {
	if row.State != "pending" {
		return nil
	}
	if row.HeadDeliveryID != nil && *row.HeadDeliveryID == row.ID {
		return nil
	}
	return row.HeadDeliveryID
}

type deliverySummaryJSON struct {
	ID                  string  `json:"id"`
	EventID             string  `json:"event_id"`
	State               string  `json:"state"`
	AttemptCount        int     `json:"attempt_count"`
	NextAttemptAt       string  `json:"next_attempt_at"`
	BlockedOnDeliveryID *string `json:"blocked_on_delivery_id"`
}

func serializeDeliverySummary(row deliverySummaryRow) deliverySummaryJSON {
	return deliverySummaryJSON{
		ID:                  row.ID,
		EventID:             row.EventID,
		State:               row.State,
		AttemptCount:        row.AttemptCount,
		NextAttemptAt:       row.NextAttemptAt.UTC().Format(time.RFC3339Nano),
		BlockedOnDeliveryID: computeBlockedOnDeliveryID(row),
	}
}
