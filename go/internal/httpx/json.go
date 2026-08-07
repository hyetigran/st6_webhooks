// Package httpx has the small JSON response helpers shared by every route
// handler, so the error envelope shape (Shared REST API contract:
// {"error":{"code","message"}}) is written in exactly one place.
package httpx

import (
	"encoding/json"
	"net/http"
)

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// WriteJSON writes status and body as a JSON response.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// WriteError writes the shared REST API contract's error envelope:
// {"error":{"code","message"}}.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorResponse{Error: ErrorBody{Code: code, Message: message}})
}
