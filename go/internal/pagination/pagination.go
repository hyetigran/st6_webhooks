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
