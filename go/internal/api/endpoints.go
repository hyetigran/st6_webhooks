package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"webhooks-go/internal/auth"
	"webhooks-go/internal/crypto"
	"webhooks-go/internal/httpx"
	"webhooks-go/internal/pagination"
	"webhooks-go/internal/secrets"
	"webhooks-go/internal/validation"
)

type endpointRow struct {
	ID            string
	URL           string
	EventTypes    []string
	Status        string
	SigningSecret string
	CreatedAt     time.Time
}

type endpointHealthRow struct {
	endpointRow
	QueueDepth        int64
	OldestPendingAt   *time.Time
	RecentSuccessRate *float64
}

type endpointJSON struct {
	ID         string   `json:"id"`
	URL        string   `json:"url"`
	EventTypes []string `json:"event_types"`
	Status     string   `json:"status"`
	CreatedAt  string   `json:"created_at"`
}

type endpointWithHealthJSON struct {
	endpointJSON
	QueueDepth        int64    `json:"queue_depth"`
	OldestPendingAt   *string  `json:"oldest_pending_at"`
	RecentSuccessRate *float64 `json:"recent_success_rate"`
}

func serializeEndpoint(row endpointRow) endpointJSON {
	return endpointJSON{
		ID:         row.ID,
		URL:        row.URL,
		EventTypes: row.EventTypes,
		Status:     row.Status,
		CreatedAt:  row.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func serializeEndpointWithHealth(row endpointHealthRow) endpointWithHealthJSON {
	var oldestPendingAt *string
	if row.OldestPendingAt != nil {
		s := row.OldestPendingAt.UTC().Format(time.RFC3339Nano)
		oldestPendingAt = &s
	}
	return endpointWithHealthJSON{
		endpointJSON:      serializeEndpoint(row.endpointRow),
		QueueDepth:        row.QueueDepth,
		OldestPendingAt:   oldestPendingAt,
		RecentSuccessRate: row.RecentSuccessRate,
	}
}

// healthSelect is the health subquery shared by GET /endpoints and
// GET /endpoints/:id (R-25): queue depth + oldest pending delivery + recent
// success rate over the last 50 terminal deliveries for that endpoint.
// Mirrors node/src/routes/endpoints.ts's HEALTH_SELECT. ::float8 (not
// ::numeric) so pgx can scan directly into *float64 without an intermediate
// numeric type.
const healthSelect = `
	e.id, e.url, e.event_types, e.status, e.signing_secret, e.created_at,
	(SELECT count(*) FROM deliveries d WHERE d.endpoint_id = e.id AND d.state IN ('pending', 'in_flight')) AS queue_depth,
	(SELECT min(d.created_at) FROM deliveries d WHERE d.endpoint_id = e.id AND d.state IN ('pending', 'in_flight')) AS oldest_pending_at,
	(
		SELECT avg((d.state = 'succeeded')::int)::float8
		FROM (
			SELECT state FROM deliveries d2
			WHERE d2.endpoint_id = e.id AND d2.state IN ('succeeded', 'failed')
			ORDER BY d2.created_at DESC
			LIMIT 50
		) d
	) AS recent_success_rate
`

func scanEndpointHealthRow(row pgx.Row) (endpointHealthRow, error) {
	var h endpointHealthRow
	err := row.Scan(
		&h.ID, &h.URL, &h.EventTypes, &h.Status, &h.SigningSecret, &h.CreatedAt,
		&h.QueueDepth, &h.OldestPendingAt, &h.RecentSuccessRate,
	)
	return h, err
}

type registerRequest struct {
	URL        string   `json:"url"`
	EventTypes []string `json:"event_types"`
}

func (s *Server) registerEndpoint(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "url is required")
		return
	}
	if len(req.EventTypes) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "event_types must have at least one entry")
		return
	}
	for _, et := range req.EventTypes {
		if strings.TrimSpace(et) == "" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "event_types entries must be non-empty")
			return
		}
	}

	result := validation.ValidateEndpointURL(r.Context(), req.URL)
	if !result.Allowed {
		httpx.WriteError(w, http.StatusBadRequest, "url_not_allowed", result.Reason)
		return
	}

	signingSecret := secrets.Generate("whsec")
	encrypted, err := crypto.EncryptSecret(signingSecret, s.secretEncryptionKey)
	if err != nil {
		log.Printf("registerEndpoint: encrypt secret: %v", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
		return
	}

	row := s.pool.QueryRow(r.Context(),
		`INSERT INTO endpoints (tenant_id, url, event_types, signing_secret)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, url, event_types, status, signing_secret, created_at`,
		auth.TenantID(r), req.URL, req.EventTypes, encrypted,
	)
	var ep endpointRow
	if err := row.Scan(&ep.ID, &ep.URL, &ep.EventTypes, &ep.Status, &ep.SigningSecret, &ep.CreatedAt); err != nil {
		log.Printf("registerEndpoint: insert: %v", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
		return
	}

	// signing_secret is viewable once (R-3) — the plaintext generated above,
	// not ep.SigningSecret (which is the encrypted value now stored).
	resp := struct {
		endpointJSON
		SigningSecret string `json:"signing_secret"`
	}{serializeEndpoint(ep), signingSecret}
	httpx.WriteJSON(w, http.StatusCreated, resp)
}

func (s *Server) listEndpoints(w http.ResponseWriter, r *http.Request) {
	limit := pagination.ParseLimit(r.URL.Query().Get("limit"), 20, 100)
	cursor, hasCursor := pagination.Cursor{}, false
	if raw := r.URL.Query().Get("before"); raw != "" {
		cursor, hasCursor = pagination.DecodeCursor(raw)
	}

	args := []any{auth.TenantID(r)}
	query := fmt.Sprintf("SELECT %s FROM endpoints e WHERE e.tenant_id = $1", healthSelect)
	if hasCursor {
		args = append(args, cursor.CreatedAt, cursor.ID)
		query += fmt.Sprintf(" AND (e.created_at, e.id) < ($%d, $%d)", len(args)-1, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY e.created_at DESC, e.id DESC LIMIT $%d", len(args))

	rows, err := s.pool.Query(r.Context(), query, args...)
	if err != nil {
		log.Printf("listEndpoints: query: %v", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
		return
	}
	defer rows.Close()

	var page []endpointHealthRow
	for rows.Next() {
		h, err := scanEndpointHealthRow(rows)
		if err != nil {
			log.Printf("listEndpoints: scan: %v", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
			return
		}
		page = append(page, h)
	}
	if err := rows.Err(); err != nil {
		log.Printf("listEndpoints: rows: %v", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
		return
	}

	hasMore := len(page) > limit
	if hasMore {
		page = page[:limit]
	}

	serialized := make([]endpointWithHealthJSON, len(page))
	for i, h := range page {
		serialized[i] = serializeEndpointWithHealth(h)
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
		Endpoints  []endpointWithHealthJSON `json:"endpoints"`
		NextCursor *string                  `json:"next_cursor"`
	}{Endpoints: serialized, NextCursor: nextCursor})
}

func (s *Server) getEndpoint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	row := s.pool.QueryRow(r.Context(),
		fmt.Sprintf("SELECT %s FROM endpoints e WHERE e.id = $1 AND e.tenant_id = $2", healthSelect),
		id, auth.TenantID(r),
	)
	h, err := scanEndpointHealthRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "Endpoint not found")
		return
	}
	if err != nil {
		log.Printf("getEndpoint: %v", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, serializeEndpointWithHealth(h))
}

func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request, newStatus string) (endpointRow, bool) {
	id := r.PathValue("id")
	row := s.pool.QueryRow(r.Context(),
		`UPDATE endpoints SET status = $1
		 WHERE id = $2 AND tenant_id = $3
		 RETURNING id, url, event_types, status, signing_secret, created_at`,
		newStatus, id, auth.TenantID(r),
	)
	var ep endpointRow
	err := row.Scan(&ep.ID, &ep.URL, &ep.EventTypes, &ep.Status, &ep.SigningSecret, &ep.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "Endpoint not found")
		return endpointRow{}, false
	}
	if err != nil {
		log.Printf("updateStatus(%s): %v", newStatus, err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
		return endpointRow{}, false
	}
	return ep, true
}

func (s *Server) pauseEndpoint(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.updateStatus(w, r, "paused")
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, serializeEndpoint(ep))
}

func (s *Server) resumeEndpoint(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.updateStatus(w, r, "active")
	if !ok {
		return
	}

	// R-14: resuming is an explicit operator action that states the
	// ordering consequence. Both parts below are checked regardless of
	// which status this endpoint was resuming from (halted or merely
	// paused) — a single code path means there's no way to reach "active"
	// without both being surfaced (REVIEW.md F-12).
	var pendingCount int64
	err := s.pool.QueryRow(r.Context(),
		`SELECT count(*) FROM deliveries WHERE endpoint_id = $1 AND state IN ('pending', 'in_flight')`,
		ep.ID,
	).Scan(&pendingCount)
	if err != nil {
		log.Printf("resumeEndpoint: pending count: %v", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
		return
	}

	// A 'failed' delivery is terminal — the claim query never reconsiders
	// it. Resuming does not retry it; only an explicit replay would, and a
	// replay appends at the tail (out of original order), not in place.
	// Anything failed *older than* the oldest still-pending delivery is
	// being left behind by this resume action, not just historical noise.
	rows, err := s.pool.Query(r.Context(),
		`SELECT id FROM deliveries
		 WHERE endpoint_id = $1 AND state = 'failed'
		   AND created_at < COALESCE(
		     (SELECT MIN(created_at) FROM deliveries WHERE endpoint_id = $1 AND state IN ('pending', 'in_flight')),
		     'infinity'
		   )
		 ORDER BY created_at`,
		ep.ID,
	)
	if err != nil {
		log.Printf("resumeEndpoint: skipped ids: %v", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
		return
	}
	defer rows.Close()

	skippedIDs := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			log.Printf("resumeEndpoint: scan skipped id: %v", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
			return
		}
		skippedIDs = append(skippedIDs, id)
	}
	if err := rows.Err(); err != nil {
		log.Printf("resumeEndpoint: rows: %v", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, struct {
		endpointJSON
		PendingDeliveryCount     int64    `json:"pending_delivery_count"`
		SkippedFailedDeliveryIDs []string `json:"skipped_failed_delivery_ids"`
	}{serializeEndpoint(ep), pendingCount, skippedIDs})
}

func (s *Server) rotateSecret(w http.ResponseWriter, r *http.Request) {
	newSecret := secrets.Generate("whsec")
	overlapExpiresAt := time.Now().Add(time.Duration(s.secretRotation.OverlapHours) * time.Hour)

	encrypted, err := crypto.EncryptSecret(newSecret, s.secretEncryptionKey)
	if err != nil {
		log.Printf("rotateSecret: encrypt: %v", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
		return
	}

	// docs/adr/0003: the current secret moves to secondary_secret and stays
	// active (the sender signs with both) until overlap_expires_at — not a
	// receiver-only dual-check, which can't work before the receiver has the
	// new secret in hand.
	var id string
	err = s.pool.QueryRow(r.Context(),
		`UPDATE endpoints
		 SET secondary_secret = signing_secret,
		     secondary_secret_expires_at = $1,
		     signing_secret = $2
		 WHERE id = $3 AND tenant_id = $4
		 RETURNING id`,
		overlapExpiresAt, encrypted, r.PathValue("id"), auth.TenantID(r),
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "Endpoint not found")
		return
	}
	if err != nil {
		log.Printf("rotateSecret: update: %v", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, struct {
		SigningSecret    string `json:"signing_secret"`
		OverlapExpiresAt string `json:"overlap_expires_at"`
	}{newSecret, overlapExpiresAt.UTC().Format(time.RFC3339Nano)})
}
