package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
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

const endpointColumns = "id, url, event_types, status, signing_secret, created_at"

func scanEndpointRow(row pgx.Row) (endpointRow, error) {
	var ep endpointRow
	err := row.Scan(&ep.ID, &ep.URL, &ep.EventTypes, &ep.Status, &ep.SigningSecret, &ep.CreatedAt)
	return ep, err
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

// fail writes the response for a query error: pgx.ErrNoRows becomes a 404
// with notFoundMsg (a no-op for queries that never produce ErrNoRows, e.g.
// count(*)), anything else becomes a logged 500. Returns true if it wrote a
// response — callers should return immediately when it does.
func fail(w http.ResponseWriter, logContext string, err error, notFoundMsg string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", notFoundMsg)
		return true
	}
	log.Printf("%s: %v", logContext, err)
	httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
	return true
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
	// Mirrors node/src/routes/endpoints.ts's zod schema exactly:
	// z.string().min(1) / z.array(z.string().min(1)).min(1) — a raw length
	// check, not a trimmed one, so whitespace-only values are NOT rejected
	// here (they fail later, in validateEndpointUrl, the same way Node's
	// does).
	if req.URL == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "url is required")
		return
	}
	if len(req.EventTypes) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "event_types must have at least one entry")
		return
	}
	for _, et := range req.EventTypes {
		if et == "" {
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

	ep, err := scanEndpointRow(s.pool.QueryRow(r.Context(),
		`INSERT INTO endpoints (tenant_id, url, event_types, signing_secret)
		 VALUES ($1, $2, $3, $4)
		 RETURNING `+endpointColumns,
		auth.TenantID(r), req.URL, req.EventTypes, encrypted,
	))
	if fail(w, "registerEndpoint", err, "") {
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
	if fail(w, "listEndpoints: query", err, "") {
		return
	}
	defer rows.Close()

	var page []endpointHealthRow
	for rows.Next() {
		h, err := scanEndpointHealthRow(rows)
		if fail(w, "listEndpoints: scan", err, "") {
			return
		}
		page = append(page, h)
	}
	if err := rows.Err(); fail(w, "listEndpoints: rows", err, "") {
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
	h, err := scanEndpointHealthRow(s.pool.QueryRow(r.Context(),
		fmt.Sprintf("SELECT %s FROM endpoints e WHERE e.id = $1 AND e.tenant_id = $2", healthSelect),
		r.PathValue("id"), auth.TenantID(r),
	))
	if fail(w, "getEndpoint", err, "Endpoint not found") {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, serializeEndpointWithHealth(h))
}

func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request, newStatus string) (endpointRow, bool) {
	ep, err := scanEndpointRow(s.pool.QueryRow(r.Context(),
		`UPDATE endpoints SET status = $1
		 WHERE id = $2 AND tenant_id = $3
		 RETURNING `+endpointColumns,
		newStatus, r.PathValue("id"), auth.TenantID(r),
	))
	if fail(w, fmt.Sprintf("updateStatus(%s)", newStatus), err, "Endpoint not found") {
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
	if fail(w, "resumeEndpoint: pending count", err, "") {
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
	if fail(w, "resumeEndpoint: skipped ids", err, "") {
		return
	}
	defer rows.Close()

	skippedIDs := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); fail(w, "resumeEndpoint: scan skipped id", err, "") {
			return
		}
		skippedIDs = append(skippedIDs, id)
	}
	if err := rows.Err(); fail(w, "resumeEndpoint: rows", err, "") {
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
	if fail(w, "rotateSecret: update", err, "Endpoint not found") {
		return
	}

	httpx.WriteJSON(w, http.StatusOK, struct {
		SigningSecret    string `json:"signing_secret"`
		OverlapExpiresAt string `json:"overlap_expires_at"`
	}{newSecret, overlapExpiresAt.UTC().Format(time.RFC3339Nano)})
}

// listEndpointDeliveries is §7 surface 4: ordered deliveries for one
// endpoint, head highlighted. Ascending by seq (head first, the actionable
// item) — deliberately the opposite direction from every other list
// route's newest-first paging, so this uses its own after/seq-based
// cursor (docs/adr/0007) rather than the shared before/created_at+id one:
// reusing created_at here would hit the exact same same-endpoint tie risk
// that column was added to avoid.
func (s *Server) listEndpointDeliveries(w http.ResponseWriter, r *http.Request) {
	endpointID := r.PathValue("id")
	var foundEndpointID string
	err := s.pool.QueryRow(r.Context(),
		"SELECT id FROM endpoints WHERE id = $1 AND tenant_id = $2", endpointID, auth.TenantID(r),
	).Scan(&foundEndpointID)
	if fail(w, "listEndpointDeliveries: endpoint lookup", err, "Endpoint not found") {
		return
	}

	limit := pagination.ParseLimit(r.URL.Query().Get("limit"), 20, 100)
	cursor, hasCursor := pagination.SeqCursor{}, false
	if raw := r.URL.Query().Get("after"); raw != "" {
		cursor, hasCursor = pagination.DecodeSeqCursor(raw)
	}

	args := []any{endpointID}
	query := `SELECT d.id, d.event_id, d.state, d.attempt_count, d.next_attempt_at, d.seq, ` + headDeliverySelect + `
	          FROM deliveries d WHERE d.endpoint_id = $1`
	if hasCursor {
		args = append(args, cursor.Seq)
		query += fmt.Sprintf(" AND d.seq > $%d", len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY d.seq ASC LIMIT $%d", len(args))

	rows, err := s.pool.Query(r.Context(), query, args...)
	if fail(w, "listEndpointDeliveries: query", err, "") {
		return
	}
	defer rows.Close()

	type queueRow struct {
		deliverySummaryRow
		Seq int64
	}
	var page []queueRow
	for rows.Next() {
		var row queueRow
		if err := rows.Scan(&row.ID, &row.EventID, &row.State, &row.AttemptCount, &row.NextAttemptAt, &row.Seq, &row.HeadDeliveryID); fail(w, "listEndpointDeliveries: scan", err, "") {
			return
		}
		page = append(page, row)
	}
	if err := rows.Err(); fail(w, "listEndpointDeliveries: rows", err, "") {
		return
	}

	hasMore := len(page) > limit
	if hasMore {
		page = page[:limit]
	}

	serialized := make([]deliverySummaryJSON, len(page))
	for i, row := range page {
		serialized[i] = serializeDeliverySummary(row.deliverySummaryRow)
	}

	var nextCursor *string
	if hasMore && len(page) > 0 {
		last := page[len(page)-1]
		c := pagination.EncodeSeqCursor(pagination.SeqCursor{Seq: last.Seq})
		nextCursor = &c
	}

	httpx.WriteJSON(w, http.StatusOK, struct {
		Deliveries []deliverySummaryJSON `json:"deliveries"`
		NextCursor *string               `json:"next_cursor"`
	}{Deliveries: serialized, NextCursor: nextCursor})
}
