package broker

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// The broker uses its own JSON envelope, distinct from rest's
// {success,data,error} (decision 10). Every endpoint returns either a bare
// success body or the error shape below (mirrors _lib/http.ts):
//
//	{ "error": { "code": "...", "message": "..." } }
//
// Responses never echo tokens, secrets, or stack traces.

// brokerError is the error envelope payload (mirrors http.ts jsonError).
type brokerError struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// defaultReadJSONMaxBytes bounds request bodies (mirrors http.ts readJson's
// 8 KiB default). Anything larger is treated as a bad request by the caller.
const defaultReadJSONMaxBytes = 8 * 1024

// defaultExpiresIn is the conservative fallback when the IdP omits or returns an
// invalid expires_in (mirrors http.ts normalizeExpiresIn fallbackSeconds=300).
const defaultExpiresIn = 300

// writeJSON writes v as a JSON body with the given status. Endpoints are never
// cached (they carry sensitive bodies), mirroring http.ts JSON_HEADERS.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes the {error:{code,message}} envelope (mirrors http.ts
// jsonError).
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, brokerError{Error: errorBody{Code: code, Message: message}})
}

// badRequest writes a 400 with the given message and code (default BAD_REQUEST),
// mirroring http.ts badRequest.
func badRequest(w http.ResponseWriter, message string, code ...string) {
	c := "BAD_REQUEST"
	if len(code) > 0 && code[0] != "" {
		c = code[0]
	}
	writeError(w, http.StatusBadRequest, c, message)
}

// notFound writes a 404 with the given code/message (mirrors http.ts notFound).
func notFound(w http.ResponseWriter, code, message string) {
	writeError(w, http.StatusNotFound, code, message)
}

// readJSON safely decodes a JSON object body into a map, returning (nil, false)
// on a non-JSON content type, an empty/oversized body, or a parse error
// (mirrors http.ts readJson: the caller turns a nil result into a 400). It never
// reads more than maxBytes.
func readJSON(r *http.Request, maxBytes int64) (map[string]any, bool) {
	if maxBytes <= 0 {
		maxBytes = defaultReadJSONMaxBytes
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if !strings.Contains(ct, "application/json") {
		return nil, false
	}
	// Read up to maxBytes+1 so an over-limit body is detected without buffering
	// an unbounded amount (mirrors the raw.length > maxBytes check in http.ts).
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return nil, false
	}
	if len(raw) == 0 || int64(len(raw)) > maxBytes {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return nil, false
	}
	return out, true
}

// readStringField reads a required non-empty string field, rejecting missing,
// non-string, empty, or over-long values (mirrors http.ts readStringField). It
// returns ("", false) so callers map failure to a 400.
func readStringField(body map[string]any, key string, maxLen int) (string, bool) {
	if body == nil {
		return "", false
	}
	if maxLen <= 0 {
		maxLen = 4096
	}
	v, ok := body[key].(string)
	if !ok || v == "" || len(v) > maxLen {
		return "", false
	}
	return v, true
}

// normalizeExpiresIn coerces an IdP expires_in to a positive integer number of
// seconds, falling back to defaultExpiresIn on absent/invalid/<=0 values
// (mirrors http.ts normalizeExpiresIn). JSON numbers decode as float64.
func normalizeExpiresIn(value any) int {
	var n float64
	switch v := value.(type) {
	case float64:
		n = v
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case string:
		// Some IdPs return expires_in as a JSON string (e.g. "3600"); mirror
		// the TS Number() coercion. Unparseable -> fallback.
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return defaultExpiresIn
		}
		n = f
	default:
		return defaultExpiresIn
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return defaultExpiresIn
	}
	floored := int(math.Floor(n))
	if floored > 0 {
		return floored
	}
	return defaultExpiresIn
}
