package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"webhooks-go/internal/auth"
	"webhooks-go/internal/httpx"
)

type publishRequest struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func (s *Server) publishEvent(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key header required")
		return
	}

	var req publishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}
	if req.Type == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "type is required")
		return
	}
	// Mirrors node/src/routes/events.ts's z.record(z.unknown()) — payload
	// must be present and a JSON object, not an array/string/number/null.
	var asObject map[string]json.RawMessage
	if len(req.Payload) == 0 || json.Unmarshal(req.Payload, &asObject) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "payload must be an object")
		return
	}

	// ON CONFLICT DO NOTHING returns zero rows on a conflict — the repeated
	// call must return the *original* event's id/status, so a conflict
	// needs a follow-up SELECT rather than trusting the INSERT's RETURNING
	// (same bug class as REVIEW.md F-11 in the Node stack).
	var id, status string
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO events (tenant_id, idempotency_key, type, payload)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
		 RETURNING id, status`,
		auth.TenantID(r), idempotencyKey, req.Type, string(req.Payload),
	).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.pool.QueryRow(r.Context(),
			`SELECT id, status FROM events WHERE tenant_id = $1 AND idempotency_key = $2`,
			auth.TenantID(r), idempotencyKey,
		).Scan(&id, &status)
	}
	if fail(w, "publishEvent", err, "") {
		return
	}

	httpx.WriteJSON(w, http.StatusAccepted, struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}{id, status})
}
