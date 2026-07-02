package broker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNormalizeExpiresIn mirrors http.test.ts normalizeExpiresIn.
func TestNormalizeExpiresIn(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{float64(3600), 3600},
		{float64(0), defaultExpiresIn},
		{float64(-5), defaultExpiresIn},
		{float64(120.9), 120}, // floored
		{nil, defaultExpiresIn},
		{"not a number", defaultExpiresIn},
		{"3600", 3600}, // stringified number (some IdPs return expires_in as a string)
		{"120.9", 120}, // stringified, floored
		{"0", defaultExpiresIn},
	}
	for _, c := range cases {
		if got := normalizeExpiresIn(c.in); got != c.want {
			t.Errorf("normalizeExpiresIn(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestReadStringField mirrors http.test.ts readStringField.
func TestReadStringField(t *testing.T) {
	body := map[string]any{"ok": "value", "empty": "", "num": float64(1)}
	if v, ok := readStringField(body, "ok", 4096); !ok || v != "value" {
		t.Errorf("readStringField ok = %q,%v", v, ok)
	}
	if _, ok := readStringField(body, "empty", 4096); ok {
		t.Error("empty string field should be rejected")
	}
	if _, ok := readStringField(body, "num", 4096); ok {
		t.Error("non-string field should be rejected")
	}
	if _, ok := readStringField(body, "missing", 4096); ok {
		t.Error("missing field should be rejected")
	}
	if _, ok := readStringField(body, "ok", 3); ok {
		t.Error("over-long field should be rejected")
	}
	if _, ok := readStringField(nil, "ok", 4096); ok {
		t.Error("nil body should be rejected")
	}
}

// TestReadJSON mirrors http.test.ts readJson: rejects non-JSON, empty, oversized.
func TestReadJSON(t *testing.T) {
	// valid JSON.
	req := jsonPost("https://x/", map[string]any{"a": "b"})
	m, ok := readJSON(req, defaultReadJSONMaxBytes)
	if !ok || m["a"] != "b" {
		t.Errorf("valid readJSON = %v,%v", m, ok)
	}

	// non-JSON content type.
	req2 := httptest.NewRequest(http.MethodPost, "https://x/", strings.NewReader(`{"a":"b"}`))
	req2.Header.Set("Content-Type", "text/plain")
	if _, ok := readJSON(req2, defaultReadJSONMaxBytes); ok {
		t.Error("non-JSON content type should be rejected")
	}

	// empty body.
	req3 := httptest.NewRequest(http.MethodPost, "https://x/", strings.NewReader(""))
	req3.Header.Set("Content-Type", "application/json")
	if _, ok := readJSON(req3, defaultReadJSONMaxBytes); ok {
		t.Error("empty body should be rejected")
	}

	// oversized body.
	big := "{\"a\":\"" + strings.Repeat("x", 100) + "\"}"
	req4 := httptest.NewRequest(http.MethodPost, "https://x/", strings.NewReader(big))
	req4.Header.Set("Content-Type", "application/json")
	if _, ok := readJSON(req4, 10); ok {
		t.Error("oversized body should be rejected")
	}

	// malformed JSON.
	req5 := httptest.NewRequest(http.MethodPost, "https://x/", strings.NewReader("{not json"))
	req5.Header.Set("Content-Type", "application/json")
	if _, ok := readJSON(req5, defaultReadJSONMaxBytes); ok {
		t.Error("malformed JSON should be rejected")
	}
}

// TestErrorEnvelopeShape verifies the broker error shape {error:{code,message}}.
func TestErrorEnvelopeShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, "BAD_REQUEST", "oops")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	e := decodeError(t, rec.Body)
	if e.Code != "BAD_REQUEST" || e.Message != "oops" {
		t.Errorf("error = %+v", e)
	}
}
