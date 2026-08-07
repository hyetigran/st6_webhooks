package api

import (
	"net/http"
	"time"

	"webhooks-go/internal/auth"
	"webhooks-go/internal/httpx"
)

// maxAttemptsInResponse is an explicit contract cap (Shared REST API
// contract ticket: "no pagination needed, capped at 6 attempts"),
// independent of config.Backoff.MaxAttempts (also 6 by default, but the two
// aren't the same promise) — this route must cap regardless of what that
// config is set to.
const maxAttemptsInResponse = 6

type attemptJSON struct {
	AttemptNumber         int     `json:"attempt_number"`
	SentAt                *string `json:"sent_at"`
	ResponseStatus        *int    `json:"response_status"`
	ResponseBodyTruncated *string `json:"response_body_truncated"`
	DurationMs            *int    `json:"duration_ms"`
	ErrorClass            *string `json:"error_class"`
}

type lastResponseJSON struct {
	ResponseStatus        *int    `json:"response_status"`
	ResponseBodyTruncated *string `json:"response_body_truncated"`
	DurationMs            *int    `json:"duration_ms"`
	ErrorClass            *string `json:"error_class"`
}

func (s *Server) getDelivery(w http.ResponseWriter, r *http.Request) {
	var row struct {
		deliverySummaryRow
		EndpointID string
	}
	err := s.pool.QueryRow(r.Context(),
		`SELECT d.id, d.event_id, d.endpoint_id, d.state, d.attempt_count, d.next_attempt_at, `+headDeliverySelect+`
		 FROM deliveries d
		 JOIN endpoints e ON e.id = d.endpoint_id
		 WHERE d.id = $1 AND e.tenant_id = $2`,
		r.PathValue("id"), auth.TenantID(r),
	).Scan(&row.ID, &row.EventID, &row.EndpointID, &row.State, &row.AttemptCount, &row.NextAttemptAt, &row.HeadDeliveryID)
	if fail(w, "getDelivery", err, "Delivery not found") {
		return
	}

	// Fetch newest-first so the cap keeps the most recent attempts (not the
	// first 6, in case config ever allows more attempts than the response
	// cap), then reverse for a chronological-order response.
	rows, err := s.pool.Query(r.Context(),
		`SELECT attempt_number, sent_at, response_status, response_body_truncated, duration_ms, error_class
		 FROM attempts WHERE delivery_id = $1 ORDER BY attempt_number DESC LIMIT $2`,
		row.ID, maxAttemptsInResponse,
	)
	if fail(w, "getDelivery: attempts", err, "") {
		return
	}
	defer rows.Close()

	var latestFirst []attemptJSON
	for rows.Next() {
		var attemptNumber int
		var sentAt *time.Time
		var responseStatus, durationMs *int
		var responseBodyTruncated, errorClass *string
		if err := rows.Scan(&attemptNumber, &sentAt, &responseStatus, &responseBodyTruncated, &durationMs, &errorClass); fail(w, "getDelivery: scan attempt", err, "") {
			return
		}
		var sentAtStr *string
		if sentAt != nil {
			s := sentAt.UTC().Format(time.RFC3339Nano)
			sentAtStr = &s
		}
		latestFirst = append(latestFirst, attemptJSON{
			AttemptNumber:         attemptNumber,
			SentAt:                sentAtStr,
			ResponseStatus:        responseStatus,
			ResponseBodyTruncated: responseBodyTruncated,
			DurationMs:            durationMs,
			ErrorClass:            errorClass,
		})
	}
	if err := rows.Err(); fail(w, "getDelivery: rows", err, "") {
		return
	}

	var lastResponse *lastResponseJSON
	if len(latestFirst) > 0 {
		last := latestFirst[0]
		lastResponse = &lastResponseJSON{
			ResponseStatus:        last.ResponseStatus,
			ResponseBodyTruncated: last.ResponseBodyTruncated,
			DurationMs:            last.DurationMs,
			ErrorClass:            last.ErrorClass,
		}
	}

	attempts := make([]attemptJSON, len(latestFirst))
	for i, a := range latestFirst {
		attempts[len(latestFirst)-1-i] = a
	}
	if attempts == nil {
		attempts = []attemptJSON{}
	}

	httpx.WriteJSON(w, http.StatusOK, struct {
		deliverySummaryJSON
		EndpointID   string            `json:"endpoint_id"`
		LastResponse *lastResponseJSON `json:"last_response"`
		Attempts     []attemptJSON     `json:"attempts"`
	}{serializeDeliverySummary(row.deliverySummaryRow), row.EndpointID, lastResponse, attempts})
}
