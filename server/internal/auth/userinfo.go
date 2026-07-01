package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// userinfoTimeout bounds the userinfo call so a slow IdP never blocks login;
// failure degrades gracefully to claims-only (contract §2.7 兜底).
const userinfoTimeout = 3 * time.Second

// userinfoClaims is the subset of userinfo response fields we consume.
type userinfoClaims struct {
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Picture           string `json:"picture"`
}

// ProfileEnricher fetches missing name/picture from the userinfo endpoint
// (contract §2.7). The endpoint is discovered from the issuer via OIDC
// discovery, or taken from an explicit override URL. It is optional: a nil
// enricher means claims-only.
type ProfileEnricher struct {
	// discovered userinfo endpoint (from OIDC discovery); empty when overridden.
	userinfoURL string
	httpClient  *http.Client
}

// NewProfileEnricher constructs an enricher. When overrideURL is non-empty it
// is used directly; otherwise the endpoint is discovered from issuer. Discovery
// failure returns an error so the caller can decide whether to proceed
// claims-only.
func NewProfileEnricher(ctx context.Context, issuer, overrideURL string) (*ProfileEnricher, error) {
	client := &http.Client{Timeout: userinfoTimeout}
	if strings.TrimSpace(overrideURL) != "" {
		return &ProfileEnricher{userinfoURL: overrideURL, httpClient: client}, nil
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery 失败: %w", err)
	}
	return &ProfileEnricher{userinfoURL: provider.UserInfoEndpoint(), httpClient: client}, nil
}

// Enrich fills empty display_name / avatar_url on p by calling userinfo with
// the caller's access token, then re-applying the §2.7 fallback rules. On any
// error it returns p unchanged (degrade, do not block login). It is a no-op
// when both fields are already populated.
func (e *ProfileEnricher) Enrich(ctx context.Context, rawToken string, p Profile) Profile {
	if e == nil || e.userinfoURL == "" {
		return p
	}
	// Only call out when something is actually missing. display_name falls back
	// to sub, so "missing name" means it currently equals the subject.
	if p.AvatarURL != "" && p.DisplayName != p.Subject {
		return p
	}

	ctx, cancel := context.WithTimeout(ctx, userinfoTimeout)
	defer cancel()

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: rawToken})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.userinfoURL, nil)
	if err != nil {
		return p
	}
	tok, err := ts.Token()
	if err != nil {
		return p
	}
	tok.SetAuthHeader(req)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return p
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return p
	}

	var uc userinfoClaims
	if err := json.NewDecoder(resp.Body).Decode(&uc); err != nil {
		return p
	}

	name := strings.TrimSpace(uc.Name)
	if name == "" {
		name = strings.TrimSpace(uc.PreferredUsername)
	}
	out := p
	if p.DisplayName == p.Subject && name != "" {
		out.DisplayName = name
	}
	if p.AvatarURL == "" && strings.TrimSpace(uc.Picture) != "" {
		out.AvatarURL = strings.TrimSpace(uc.Picture)
	}
	return out
}
