package config

import (
	"strings"
	"testing"
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
	}
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	// Clear all LUMEN_* keys we care about so tests are isolated.
	for _, k := range []string{
		"LUMEN_OAUTH_ISSUER", "LUMEN_OAUTH_JWKS_URL", "LUMEN_OAUTH_USERINFO_URL",
		"LUMEN_OAUTH_AUDIENCE", "LUMEN_OWNER_SUBJECTS", "LUMEN_LISTEN_ADDR",
		"LUMEN_DATABASE_URL", "LUMEN_PUBLIC_IP", "LUMEN_WEBRTC_UDP_PORT",
		"LUMEN_PUBLIC_WS_URL", "LUMEN_UPDATES_DIR", "LUMEN_LOG_LEVEL",
	} {
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
