// Package api wires the HTTP surface: stdlib net/http (Go 1.22+ method+
// wildcard ServeMux patterns), the requireTenant auth middleware, and the
// endpoint-management handlers. Mirrors node/src/app.ts's route table and
// error envelope exactly (Shared REST API contract).
package api

import (
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"webhooks-go/internal/auth"
	"webhooks-go/internal/config"
	"webhooks-go/internal/httpx"
)

type Server struct {
	pool                *pgxpool.Pool
	secretEncryptionKey []byte
	secretRotation      config.SecretRotationConfig
}

func NewServer(pool *pgxpool.Pool, secretEncryptionKey []byte, secretRotation config.SecretRotationConfig) *Server {
	return &Server{
		pool:                pool,
		secretEncryptionKey: secretEncryptionKey,
		secretRotation:      secretRotation,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	requireTenant := auth.RequireTenant(s.pool)
	mux.Handle("POST /endpoints", requireTenant(http.HandlerFunc(s.registerEndpoint)))
	mux.Handle("GET /endpoints", requireTenant(http.HandlerFunc(s.listEndpoints)))
	mux.Handle("GET /endpoints/{id}", requireTenant(http.HandlerFunc(s.getEndpoint)))
	mux.Handle("POST /endpoints/{id}/pause", requireTenant(http.HandlerFunc(s.pauseEndpoint)))
	mux.Handle("POST /endpoints/{id}/resume", requireTenant(http.HandlerFunc(s.resumeEndpoint)))
	mux.Handle("POST /endpoints/{id}/secret/rotate", requireTenant(http.HandlerFunc(s.rotateSecret)))

	// Catch-all: any path none of the above patterns matched.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "Not found")
	})

	return recoverJSON(mux)
}

// recoverJSON mirrors app.ts's final Express error-handling middleware — a
// panic (Express: a thrown/rejected error) becomes a 500 JSON envelope
// instead of crashing the process or leaking a bare stack trace.
func recoverJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic handling %s %s: %v", r.Method, r.URL.Path, rec)
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
