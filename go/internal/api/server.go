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
	corsOrigin          string
}

// NewServer holds the dependencies every handler needs: the DB pool, the
// AES-256-GCM key signing secrets are encrypted with, the rotation
// overlap window (docs/adr/0003), and the allowed CORS origin (ADR-008's
// shared frontend/ SPA).
func NewServer(pool *pgxpool.Pool, secretEncryptionKey []byte, secretRotation config.SecretRotationConfig, corsOrigin string) *Server {
	return &Server{
		pool:                pool,
		secretEncryptionKey: secretEncryptionKey,
		secretRotation:      secretRotation,
		corsOrigin:          corsOrigin,
	}
}

// Handler builds the full route table, ready to pass to http.ListenAndServe
// or httptest.NewServer.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	// Everything except /healthz sits behind requireTenant — including an
	// unmatched path, mirroring app.ts's `app.use(requireTenant,
	// endpointsRouter, ...)` running before its final 404 handler. An
	// unauthenticated request to an unknown route must still get 401, not
	// 404 (which would leak whether the route exists to a caller with no
	// valid key at all).
	protected := http.NewServeMux()
	protected.HandleFunc("POST /endpoints", s.registerEndpoint)
	protected.HandleFunc("GET /endpoints", s.listEndpoints)
	protected.HandleFunc("GET /endpoints/{id}", s.getEndpoint)
	protected.HandleFunc("POST /endpoints/{id}/pause", s.pauseEndpoint)
	protected.HandleFunc("POST /endpoints/{id}/resume", s.resumeEndpoint)
	protected.HandleFunc("POST /endpoints/{id}/secret/rotate", s.rotateSecret)
	protected.HandleFunc("POST /events", s.publishEvent)
	protected.HandleFunc("POST /endpoints/{id}/replays", s.createReplay)
	protected.HandleFunc("GET /events", s.listEvents)
	protected.HandleFunc("GET /events/{id}", s.getEvent)
	protected.HandleFunc("GET /deliveries/{id}", s.getDelivery)
	protected.HandleFunc("GET /endpoints/{id}/deliveries", s.listEndpointDeliveries)
	protected.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "Not found")
	})

	mux.Handle("/", auth.RequireTenant(s.pool)(protected))

	return recoverJSON(s.cors(mux))
}

// cors must run before requireTenant: a browser's CORS preflight (OPTIONS)
// never carries the Authorization header, so requireTenant would reject it
// with 401 — this answers preflights itself and never forwards them.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.corsOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "authorization, content-type, idempotency-key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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
