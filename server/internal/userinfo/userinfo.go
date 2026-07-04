// Package userinfo flexibly extracts a normalized identity from an OAuth2 /
// OIDC userinfo response. OIDC providers use standard claim names (sub, name,
// preferred_username, picture); plain-OAuth2 providers (e.g. Nanako OAuth) use
// their own (id, username, avatar). This package tolerates both so the resource
// server and the login broker can consume either kind of IdP without hardcoding
// one provider's field names.
package userinfo

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Info is the normalized identity extracted from a userinfo response. Missing
// fields are empty strings; the caller decides whether an empty Subject is fatal.
type Info struct {
	Subject     string
	DisplayName string
	AvatarURL   string
	Email       string
}

// Field-name candidates in priority order. OIDC-standard names come first so an
// OIDC provider keeps its exact semantics; plain-OAuth2 aliases follow.
var (
	subjectKeys = []string{"sub", "id", "user_id", "userid", "uid"}
	nameKeys    = []string{"name", "preferred_username", "username", "nickname", "display_name", "displayName"}
	avatarKeys  = []string{"picture", "avatar", "avatar_url", "avatarUrl", "avatarURL", "avatar_uri"}
	emailKeys   = []string{"email", "mail"}
	// nestKeys are common envelope wrappers ({"data":{...}} / {"user":{...}}).
	nestKeys = []string{"data", "user", "userinfo", "profile", "result"}
)

// Parse extracts identity from a userinfo JSON body. It returns an error only on
// malformed JSON; a well-formed body with unrecognized fields yields a zero-value
// Info (empty Subject), which the caller treats as "no identity".
func Parse(body []byte) (Info, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return Info{}, fmt.Errorf("userinfo 响应非法 JSON: %w", err)
	}
	return FromMap(m), nil
}

// FromMap extracts identity from an already-decoded map. When the top level
// carries no subject it looks one level down into a common envelope wrapper.
func FromMap(m map[string]any) Info {
	info := extract(m)
	if info.Subject != "" {
		return info
	}
	for _, nk := range nestKeys {
		if nested, ok := m[nk].(map[string]any); ok {
			if ni := extract(nested); ni.Subject != "" {
				return ni
			}
		}
	}
	return info
}

func extract(m map[string]any) Info {
	return Info{
		Subject:     firstField(m, subjectKeys),
		DisplayName: firstField(m, nameKeys),
		AvatarURL:   firstField(m, avatarKeys),
		Email:       firstField(m, emailKeys),
	}
}

// firstField returns the first key whose value renders to a non-empty string.
func firstField(m map[string]any, keys []string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s := toString(v); s != "" {
				return s
			}
		}
	}
	return ""
}

// toString renders a JSON scalar as a string: strings verbatim (trimmed), whole
// numbers as integers (JSON numbers decode to float64), other numbers as-is.
// Non-scalar / bool values yield "" so they are never mistaken for an identifier.
func toString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return ""
	}
}
