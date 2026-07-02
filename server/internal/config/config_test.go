package config

import (
	"strings"
	"testing"
)

// Two distinct base64 AES-256 keys (32 bytes each) for the broker enc keys.
const (
	testSessionKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" // 32 zero bytes, base64
	testRefreshKey = "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=" // 32 bytes, distinct
)

// requiredEnv is a minimal valid environment for Load to succeed.
func requiredEnv() map[string]string {
	return map[string]string{
		"LUMEN_OAUTH_ISSUER":    "https://auth.example.com/realms/lumen",
		"LUMEN_OAUTH_JWKS_URL":  "https://auth.example.com/realms/lumen/protocol/openid-connect/certs",
		"LUMEN_OAUTH_AUDIENCE":  "lumen-api",
		"LUMEN_OWNER_SUBJECTS":  "sub-abc, sub-def",
		"LUMEN_LISTEN_ADDR":     "0.0.0.0:8080",
		"LUMEN_DATABASE_URL":    "postgres://lumen:pw@lumen-db:5432/lumen?sslmode=disable",
		"LUMEN_PUBLIC_IP":       "203.0.113.10",
		"LUMEN_WEBRTC_UDP_PORT": "40000",

		// Account center / desktop broker required keys.
		"LUMEN_OAUTH_CLIENT_ID":            "lumen-web",
		"LUMEN_OAUTH_CLIENT_SECRET":        "s3cr3t",
		"LUMEN_OAUTH_DESKTOP_REDIRECT_URI": "https://acct.example.com/desktop/callback",
		"LUMEN_OAUTH_WEB_REDIRECT_URI":     "https://acct.example.com/auth/callback",
		"LUMEN_WEB_BASE_URL":               "https://acct.example.com",
		"LUMEN_SESSION_ENC_KEY":            testSessionKey,
		"LUMEN_REFRESH_ENC_KEY":            testRefreshKey,
	}
}

// allKeys lists every LUMEN_* key the tests set, so each test starts isolated.
var allKeys = []string{
	"LUMEN_OAUTH_ISSUER", "LUMEN_OAUTH_JWKS_URL", "LUMEN_OAUTH_USERINFO_URL",
	"LUMEN_OAUTH_AUDIENCE", "LUMEN_OWNER_SUBJECTS", "LUMEN_LISTEN_ADDR",
	"LUMEN_DATABASE_URL", "LUMEN_PUBLIC_IP", "LUMEN_WEBRTC_UDP_PORT",
	"LUMEN_PUBLIC_WS_URL", "LUMEN_UPDATES_DIR", "LUMEN_LOG_LEVEL",
	"LUMEN_OAUTH_CLIENT_ID", "LUMEN_OAUTH_CLIENT_SECRET",
	"LUMEN_OAUTH_AUTHORIZE_URL", "LUMEN_OAUTH_TOKEN_URL",
	"LUMEN_OAUTH_DESKTOP_REDIRECT_URI", "LUMEN_OAUTH_WEB_REDIRECT_URI",
	"LUMEN_WEB_BASE_URL", "LUMEN_SESSION_ENC_KEY", "LUMEN_REFRESH_ENC_KEY",
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	// Clear all LUMEN_* keys we care about so tests are isolated.
	for _, k := range allKeys {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestLoad_Success(t *testing.T) {
	setEnv(t, requiredEnv())

	c, err := Load()
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if c.OAuthAudience != "lumen-api" {
		t.Errorf("OAuthAudience = %q, want lumen-api", c.OAuthAudience)
	}
	if c.WebRTCUDPPort != 40000 {
		t.Errorf("WebRTCUDPPort = %d, want 40000", c.WebRTCUDPPort)
	}
	// CSV split trims whitespace and drops empties.
	if len(c.OwnerSubjects) != 2 || c.OwnerSubjects[0] != "sub-abc" || c.OwnerSubjects[1] != "sub-def" {
		t.Errorf("OwnerSubjects = %v, want [sub-abc sub-def]", c.OwnerSubjects)
	}
	// Optional defaults applied.
	if c.UpdatesDir != defaultUpdatesDir {
		t.Errorf("UpdatesDir = %q, want default %q", c.UpdatesDir, defaultUpdatesDir)
	}
	if c.LogLevel != defaultLogLevel {
		t.Errorf("LogLevel = %q, want default %q", c.LogLevel, defaultLogLevel)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	env := requiredEnv()
	delete(env, "LUMEN_DATABASE_URL")
	delete(env, "LUMEN_PUBLIC_IP")
	setEnv(t, env)

	_, err := Load()
	if err == nil {
		t.Fatal("expected fail-fast error for missing required keys")
	}
	if !strings.Contains(err.Error(), "LUMEN_DATABASE_URL") {
		t.Errorf("error should list LUMEN_DATABASE_URL, got: %v", err)
	}
	if !strings.Contains(err.Error(), "LUMEN_PUBLIC_IP") {
		t.Errorf("error should list LUMEN_PUBLIC_IP, got: %v", err)
	}
}

func TestLoad_InvalidUDPPort(t *testing.T) {
	for _, bad := range []string{"0", "70000", "abc", "-1"} {
		env := requiredEnv()
		env["LUMEN_WEBRTC_UDP_PORT"] = bad
		setEnv(t, env)

		_, err := Load()
		if err == nil {
			t.Fatalf("port %q: expected error, got none", bad)
		}
		if !strings.Contains(err.Error(), "LUMEN_WEBRTC_UDP_PORT") {
			t.Errorf("port %q: error should mention LUMEN_WEBRTC_UDP_PORT, got: %v", bad, err)
		}
	}
}

func TestLoad_OptionalOverrides(t *testing.T) {
	env := requiredEnv()
	env["LUMEN_UPDATES_DIR"] = "/custom/updates"
	env["LUMEN_LOG_LEVEL"] = "debug"
	env["LUMEN_PUBLIC_WS_URL"] = "wss://chat.example.com/ws"
	setEnv(t, env)

	c, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.UpdatesDir != "/custom/updates" {
		t.Errorf("UpdatesDir = %q, want /custom/updates", c.UpdatesDir)
	}
	if c.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", c.LogLevel)
	}
	if c.PublicWSURL != "wss://chat.example.com/ws" {
		t.Errorf("PublicWSURL = %q", c.PublicWSURL)
	}
}

func TestLoad_BrokerFields(t *testing.T) {
	env := requiredEnv()
	env["LUMEN_OAUTH_AUTHORIZE_URL"] = "https://auth.example.com/authorize"
	env["LUMEN_OAUTH_TOKEN_URL"] = "https://auth.example.com/token"
	setEnv(t, env)

	c, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.OAuthClientID != "lumen-web" || c.OAuthClientSecret != "s3cr3t" {
		t.Errorf("client id/secret = %q/%q", c.OAuthClientID, c.OAuthClientSecret)
	}
	if c.OAuthDesktopRedirect != "https://acct.example.com/desktop/callback" {
		t.Errorf("desktop redirect = %q", c.OAuthDesktopRedirect)
	}
	if c.OAuthWebRedirect != "https://acct.example.com/auth/callback" {
		t.Errorf("web redirect = %q", c.OAuthWebRedirect)
	}
	if c.WebBaseURL != "https://acct.example.com" {
		t.Errorf("web base = %q", c.WebBaseURL)
	}
	// Optional authorize/token URLs pass through when present.
	if c.OAuthAuthorizeURL != "https://auth.example.com/authorize" {
		t.Errorf("authorize url = %q", c.OAuthAuthorizeURL)
	}
	if c.OAuthTokenURL != "https://auth.example.com/token" {
		t.Errorf("token url = %q", c.OAuthTokenURL)
	}
	// Decoded keys are exactly 32 bytes and distinct.
	sk, rk := c.SessionEncKey(), c.RefreshEncKey()
	if len(sk) != 32 || len(rk) != 32 {
		t.Fatalf("key lengths = %d/%d, want 32/32", len(sk), len(rk))
	}
	if bytesEqual(sk, rk) {
		t.Error("session and refresh keys must differ")
	}
	// Optional authorize/token URLs default to empty (derived via discovery).
	env2 := requiredEnv()
	setEnv(t, env2)
	c2, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c2.OAuthAuthorizeURL != "" || c2.OAuthTokenURL != "" {
		t.Errorf("authorize/token URLs should be empty when unset, got %q/%q", c2.OAuthAuthorizeURL, c2.OAuthTokenURL)
	}
}

func TestLoad_MissingBrokerRequired(t *testing.T) {
	for _, key := range []string{
		"LUMEN_OAUTH_CLIENT_ID", "LUMEN_OAUTH_CLIENT_SECRET",
		"LUMEN_OAUTH_DESKTOP_REDIRECT_URI", "LUMEN_OAUTH_WEB_REDIRECT_URI",
		"LUMEN_WEB_BASE_URL", "LUMEN_SESSION_ENC_KEY", "LUMEN_REFRESH_ENC_KEY",
	} {
		env := requiredEnv()
		delete(env, key)
		setEnv(t, env)
		_, err := Load()
		if err == nil {
			t.Fatalf("%s: expected fail-fast error", key)
		}
		if !strings.Contains(err.Error(), key) {
			t.Errorf("%s: error should mention it, got: %v", key, err)
		}
	}
}

func TestLoad_InvalidEncKey(t *testing.T) {
	// Not base64.
	env := requiredEnv()
	env["LUMEN_SESSION_ENC_KEY"] = "!!!not base64!!!"
	setEnv(t, env)
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "LUMEN_SESSION_ENC_KEY") {
		t.Errorf("non-base64 session key should fail, got: %v", err)
	}

	// Valid base64 but wrong length (16 bytes, not 32).
	env = requiredEnv()
	env["LUMEN_REFRESH_ENC_KEY"] = "MDEyMzQ1Njc4OWFiY2RlZg==" // 16 bytes
	setEnv(t, env)
	_, err = Load()
	if err == nil || !strings.Contains(err.Error(), "LUMEN_REFRESH_ENC_KEY") {
		t.Errorf("wrong-length refresh key should fail, got: %v", err)
	}
}

func TestLoad_EncKeysMustDiffer(t *testing.T) {
	env := requiredEnv()
	env["LUMEN_REFRESH_ENC_KEY"] = env["LUMEN_SESSION_ENC_KEY"] // identical
	setEnv(t, env)
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "LUMEN_REFRESH_ENC_KEY") {
		t.Errorf("identical enc keys should fail, got: %v", err)
	}
}
