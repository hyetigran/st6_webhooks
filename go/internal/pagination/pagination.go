// Package pagination implements cursor-based pagination keyed on
// created_at+id (Shared REST API contract) — avoids the skip-based
// consistency issues offset/limit would have against tables that are being
// inserted into continuously. Mirrors node/src/lib/pagination.ts.
package pagination

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
)

type Cursor struct {
	CreatedAt string `json:"createdAt"`
	ID        string `json:"id"`
}

// EncodeCursor opaquely encodes a cursor for the "next_cursor" response
// field / "before" query param round trip.
func EncodeCursor(c Cursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor returns (Cursor{}, false) for anything malformed — callers
// treat a bad cursor as "no cursor" rather than a hard error.
func DecodeCursor(raw string) (Cursor, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, false
	}
	var c Cursor
	if err := json.Unmarshal(decoded, &c); err != nil {
		return Cursor{}, false
	}
	if c.CreatedAt == "" || c.ID == "" {
		return Cursor{}, false
	}
	return c, true
}

// ParseLimit parses a "limit" query param, falling back to fallback for
// anything empty, non-numeric, or non-positive, and capping at max.
func ParseLimit(raw string, fallback, max int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}

// SeqCursor paginates GET /endpoints/:id/deliveries — deliveries.seq
// (docs/adr/0007) is already unique and monotonic, so a delivery-queue
// cursor needs no id tiebreak the way Cursor's created_at+id does. This is
// also why it's a distinct type from Cursor: reusing created_at here would
// hit the exact tie risk ADR-0007 exists to avoid (Postgres's now() is
// transaction-stable; a bulk same-endpoint insert can give several rows the
// same created_at).
type SeqCursor struct {
	Seq int64 `json:"seq"`
}

// EncodeSeqCursor opaquely encodes a cursor for the "next_cursor" response
// field / "after" query param round trip.
func EncodeSeqCursor(c SeqCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeSeqCursor returns (SeqCursor{}, false) for anything malformed —
// callers treat a bad cursor as "no cursor" rather than a hard error.
func DecodeSeqCursor(raw string) (SeqCursor, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return SeqCursor{}, false
	}
	var c SeqCursor
	if err := json.Unmarshal(decoded, &c); err != nil {
		return SeqCursor{}, false
	}
	// deliveries.seq is a BIGSERIAL starting at 1 — a non-positive value
	// can never be a real cursor, only malformed/garbage input.
	if c.Seq <= 0 {
		return SeqCursor{}, false
	}
	return c, true
}
