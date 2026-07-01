// Package rest implements the REST API (contract §3, server-design §5.4). All
// responses use one envelope; errors map to the shared error-code table
// (contract §7.2). Handlers depend on auth and store, and on the signaling
// Broadcaster interface for owner side effects — never on sfu directly.
package rest

import (
	"encoding/json"
	"net/http"
)

// Envelope is the uniform REST response wrapper (contract §3.2).
type Envelope struct {
	Success bool      `json:"success"`
	Data    any       `json:"data"`
	Error   *APIError `json:"error"`
}

// APIError is the error payload (contract §3.2). Details is optional
// field-level validation info.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

// writeJSON writes v as JSON with the given status and content type.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeOK writes a success envelope.
func writeOK(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, Envelope{Success: true, Data: data, Error: nil})
}

// writeErr writes an error envelope with a machine code and user message.
func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, Envelope{
		Success: false,
		Data:    nil,
		Error:   &APIError{Code: code, Message: message},
	})
}

// writeErrDetails writes an error envelope with field-level details.
func writeErrDetails(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, Envelope{
		Success: false,
		Data:    nil,
		Error:   &APIError{Code: code, Message: message, Details: details},
	})
}
