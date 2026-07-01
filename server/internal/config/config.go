// Package config loads and validates all runtime configuration from
// environment variables (contract §1.3, server-design §6.1). Missing required
// values cause a fail-fast error listing every offending key.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Default values for optional settings.
const (
	defaultUpdatesDir = "/app/updates"
	defaultLogLevel   = "info"
)

// Config is the immutable runtime configuration. Once loaded it is never
// mutated; callers treat it as read-only.
type Config struct {
	OAuthIssuer      string
	OAuthJWKSURL     string
	OAuthUserinfoURL string // optional; derived via OIDC discovery when empty
	OAuthAudience    string
	OwnerSubjects    []string
	ListenAddr       string
	DatabaseURL      string
	PublicIP         string
	WebRTCUDPPort    int
	PublicWSURL      string // optional; derived from Host header when empty
	UpdatesDir       string
	LogLevel         string
}

// Load reads configuration from the environment and validates required keys.
// On any missing or invalid required value it returns an aggregated error
// (fail-fast) and a zero Config.
func Load() (Config, error) {
	var missing []string
	get := func(key string, required bool) string {
		v := strings.TrimSpace(os.Getenv(key))
		if required && v == "" {
			missing = append(missing, key)
		}
		return v
	}

	c := Config{
		OAuthIssuer:      get("LUMEN_OAUTH_ISSUER", true),
		OAuthJWKSURL:     get("LUMEN_OAUTH_JWKS_URL", true),
		OAuthUserinfoURL: get("LUMEN_OAUTH_USERINFO_URL", false),
		OAuthAudience:    get("LUMEN_OAUTH_AUDIENCE", true),
		OwnerSubjects:    splitCSV(get("LUMEN_OWNER_SUBJECTS", true)),
		ListenAddr:       get("LUMEN_LISTEN_ADDR", true),
		DatabaseURL:      get("LUMEN_DATABASE_URL", true),
		PublicIP:         get("LUMEN_PUBLIC_IP", true),
		PublicWSURL:      get("LUMEN_PUBLIC_WS_URL", false),
		UpdatesDir:       orDefault(get("LUMEN_UPDATES_DIR", false), defaultUpdatesDir),
		LogLevel:         orDefault(get("LUMEN_LOG_LEVEL", false), defaultLogLevel),
	}

	portStr := get("LUMEN_WEBRTC_UDP_PORT", true)
	if portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			missing = append(missing, "LUMEN_WEBRTC_UDP_PORT(无效)")
		} else {
			c.WebRTCUDPPort = port
		}
	}

	if len(missing) > 0 {
		return Config{}, fmt.Errorf("缺失/无效环境变量: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

// splitCSV splits a comma-separated list, trimming whitespace and dropping
// empty entries.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// orDefault returns v when non-empty, otherwise def.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
