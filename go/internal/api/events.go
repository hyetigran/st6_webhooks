package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"webhooks-go/internal/auth"
	"webhooks-go/internal/httpx"
	"webhooks-go/internal/pagination"
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
	// Unmarshaling into a map specifically does NOT reject `null` (Go
	// decodes JSON null into a nil map with no error) — decoding into `any`
	// and type-asserting the result to map[string]any does, since JSON
	// null becomes a nil interface{}, not a nil map.
	var decodedPayload any
	if err := json.Unmarshal(req.Payload, &decodedPayload); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "payload must be an object")
		return
	}
	if _, ok := decodedPayload.(map[string]any); !ok {
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

type eventRow struct {
	ID        string
	Type      string
	Payload   json.RawMessage
	Status    string
	CreatedAt time.Time
}

type eventJSON struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Status    string          `json:"status"`
	CreatedAt string          `json:"created_at"`
}

func serializeEvent(row eventRow) eventJSON {
	return eventJSON{
		ID:        row.ID,
		Type:      row.Type,
		Payload:   row.Payload,
		Status:    row.Status,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// listEvents implements R-24: searchable by id/type/endpoint_id/time-range.
// endpoint_id filters via EXISTS through deliveries — an event has at most
// one delivery per endpoint (one fan-out row each), so this can't return a
// given event twice.
func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	limit := pagination.ParseLimit(r.URL.Query().Get("limit"), 20, 100)
	cursor, hasCursor := pagination.Cursor{}, false
	if raw := r.URL.Query().Get("before"); raw != "" {
		cursor, hasCursor = pagination.DecodeCursor(raw)
	}

	args := []any{auth.TenantID(r)}
	conditions := []string{"e.tenant_id = $1"}

	if id := r.URL.Query().Get("id"); id != "" {
		args = append(args, id)
		conditions = append(conditions, fmt.Sprintf("e.id = $%d", len(args)))
	}
	if eventType := r.URL.Query().Get("type"); eventType != "" {
		args = append(args, eventType)
		conditions = append(conditions, fmt.Sprintf("e.type = $%d", len(args)))
	}
	if from := r.URL.Query().Get("from"); from != "" {
		args = append(args, from)
		conditions = append(conditions, fmt.Sprintf("e.created_at >= $%d", len(args)))
	}
	if to := r.URL.Query().Get("to"); to != "" {
		args = append(args, to)
		conditions = append(conditions, fmt.Sprintf("e.created_at <= $%d", len(args)))
	}
	if endpointID := r.URL.Query().Get("endpoint_id"); endpointID != "" {
		args = append(args, endpointID)
		conditions = append(conditions, fmt.Sprintf("EXISTS (SELECT 1 FROM deliveries d WHERE d.event_id = e.id AND d.endpoint_id = $%d)", len(args)))
	}
	if hasCursor {
		args = append(args, cursor.CreatedAt, cursor.ID)
		conditions = append(conditions, fmt.Sprintf("(e.created_at, e.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)

	query := fmt.Sprintf(
		`SELECT e.id, e.type, e.payload, e.status, e.created_at
		 FROM events e
		 WHERE %s
		 ORDER BY e.created_at DESC, e.id DESC
		 LIMIT $%d`,
		strings.Join(conditions, " AND "), len(args),
	)

	rows, err := s.pool.Query(r.Context(), query, args...)
	if fail(w, "listEvents: query", err, "") {
		return
	}
	defer rows.Close()

	var page []eventRow
	for rows.Next() {
		var row eventRow
		if err := rows.Scan(&row.ID, &row.Type, &row.Payload, &row.Status, &row.CreatedAt); fail(w, "listEvents: scan", err, "") {
			return
		}
		page = append(page, row)
	}
	if err := rows.Err(); fail(w, "listEvents: rows", err, "") {
		return
	}

	hasMore := len(page) > limit
	if hasMore {
		page = page[:limit]
	}

	serialized := make([]eventJSON, len(page))
	for i, row := range page {
		serialized[i] = serializeEvent(row)
	}

	var nextCursor *string
	if hasMore && len(page) > 0 {
		last := page[len(page)-1]
		c := pagination.EncodeCursor(pagination.Cursor{
			CreatedAt: last.CreatedAt.UTC().Format(time.RFC3339Nano),
			ID:        last.ID,
		})
		nextCursor = &c
	}

	httpx.WriteJSON(w, http.StatusOK, struct {
		Events     []eventJSON `json:"events"`
		NextCursor *string     `json:"next_cursor"`
	}{Events: serialized, NextCursor: nextCursor})
}

type eventDeliveryFanoutJSON struct {
	ID            string `json:"id"`
	EndpointID    string `json:"endpoint_id"`
	State         string `json:"state"`
	AttemptCount  int    `json:"attempt_count"`
	NextAttemptAt string `json:"next_attempt_at"`
}

// getEvent is §7 surface 2: the event, its payload, and its fan-out across
// endpoints. Per-delivery detail (attempts, blocked_on_delivery_id) lives
// at its own GET /deliveries/{id} — this is a summary list, not the full
// detail.
func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	var row eventRow
	err := s.pool.QueryRow(r.Context(),
		"SELECT id, type, payload, status, created_at FROM events WHERE id = $1 AND tenant_id = $2",
		r.PathValue("id"), auth.TenantID(r),
	).Scan(&row.ID, &row.Type, &row.Payload, &row.Status, &row.CreatedAt)
	if fail(w, "getEvent", err, "Event not found") {
		return
	}

	rows, err := s.pool.Query(r.Context(),
		"SELECT id, endpoint_id, state, attempt_count, next_attempt_at FROM deliveries WHERE event_id = $1 ORDER BY seq",
		row.ID,
	)
	if fail(w, "getEvent: deliveries", err, "") {
		return
	}
	defer rows.Close()

	deliveries := []eventDeliveryFanoutJSON{}
	for rows.Next() {
		var id, endpointID, state string
		var attemptCount int
		var nextAttemptAt time.Time
		if err := rows.Scan(&id, &endpointID, &state, &attemptCount, &nextAttemptAt); fail(w, "getEvent: scan delivery", err, "") {
			return
		}
		deliveries = append(deliveries, eventDeliveryFanoutJSON{
			ID:            id,
			EndpointID:    endpointID,
			State:         state,
			AttemptCount:  attemptCount,
			NextAttemptAt: nextAttemptAt.UTC().Format(time.RFC3339Nano),
		})
	}
	if err := rows.Err(); fail(w, "getEvent: rows", err, "") {
		return
	}

	httpx.WriteJSON(w, http.StatusOK, struct {
		eventJSON
		Deliveries []eventDeliveryFanoutJSON `json:"deliveries"`
	}{serializeEvent(row), deliveries})
}
