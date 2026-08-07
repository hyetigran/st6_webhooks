package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"webhooks-go/internal/auth"
	"webhooks-go/internal/httpx"
)

type replayRequest struct {
	RangeStart string `json:"range_start"`
	RangeEnd   string `json:"range_end"`
}

// docs/adr/0005: replay mirrors publish's async two-phase shape exactly —
// this handler does one thing synchronously (insert the replays row) and
// returns 202 immediately; the durable ack is that one insert. The shared
// worker pool later expands a pending replay (internal/worker's
// RunReplayExpansionCycle).
func (s *Server) createReplay(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key header required")
		return
	}

	var req replayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}
	rangeStart, err := time.Parse(time.RFC3339, req.RangeStart)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "range_start must be an RFC3339 datetime")
		return
	}
	rangeEnd, err := time.Parse(time.RFC3339, req.RangeEnd)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "range_end must be an RFC3339 datetime")
		return
	}
	if rangeEnd.Before(rangeStart) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "range_end must not be before range_start")
		return
	}

	endpointID := r.PathValue("id")
	var exists string
	err = s.pool.QueryRow(r.Context(),
		"SELECT id FROM endpoints WHERE id = $1 AND tenant_id = $2", endpointID, auth.TenantID(r),
	).Scan(&exists)
	if fail(w, "createReplay: endpoint lookup", err, "Endpoint not found") {
		return
	}

	// ON CONFLICT DO NOTHING returns zero rows on a conflict — the repeated
	// call must return the *original* replay's id/status, so a conflict
	// needs a follow-up SELECT rather than trusting the INSERT's RETURNING
	// (same idempotency shape as POST /events).
	var id, status string
	err = s.pool.QueryRow(r.Context(),
		`INSERT INTO replays (endpoint_id, idempotency_key, range_start, range_end)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (endpoint_id, idempotency_key) DO NOTHING
		 RETURNING id, status`,
		endpointID, idempotencyKey, rangeStart, rangeEnd,
	).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.pool.QueryRow(r.Context(),
			`SELECT id, status FROM replays WHERE endpoint_id = $1 AND idempotency_key = $2`,
			endpointID, idempotencyKey,
		).Scan(&id, &status)
	}
	if fail(w, "createReplay", err, "") {
		return
	}

	httpx.WriteJSON(w, http.StatusAccepted, struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}{id, status})
}
