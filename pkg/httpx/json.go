// Package httpx contains router-agnostic HTTP helpers: JSON responses, the
// error envelope and common middleware. It must not depend on JAVIS domain code.
package httpx

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse is the error envelope shared by every endpoint:
// {"error":{"code":"...","message":"..."}}.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody carries a stable machine-readable code and a safe message.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteJSON writes payload as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Client disconnects while writing do not require logging.
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteError writes the error envelope. message must be safe to show clients.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorResponse{Error: ErrorBody{Code: code, Message: message}})
}
