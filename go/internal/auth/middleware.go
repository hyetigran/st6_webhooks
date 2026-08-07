// Package auth resolves a Bearer API key to a tenant_id, mirroring
// node/src/auth/middleware.ts.
package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"webhooks-go/internal/crypto"
	"webhooks-go/internal/httpx"
)

type contextKey int

const tenantIDKey contextKey = iota

// RequireTenant resolves the Bearer API key server-side to a tenant_id —
// never accepted as a URL/body parameter (Shared REST API contract).
func RequireTenant(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
			if !ok || scheme != "Bearer" || token == "" {
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "Missing or malformed Authorization header")
				return
			}

			var tenantID string
			err := pool.QueryRow(r.Context(), "SELECT id FROM tenants WHERE api_key_hash = $1", crypto.HashAPIKey(token)).Scan(&tenantID)
			if err != nil {
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "Invalid API key")
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tenantIDKey, tenantID)))
		})
	}
}

// TenantID reads the tenant_id set by RequireTenant. Only ever called from
// inside a handler mounted behind that middleware, so an empty return here
// would indicate a routing bug, not a real unauthenticated request.
func TenantID(r *http.Request) string {
	id, _ := r.Context().Value(tenantIDKey).(string)
	return id
}
