package auth

import (
	"strings"
	"time"

	"lumen/internal/protocol"
	"lumen/internal/store"
)

// Profile is a normalised, immutable view of user profile fields derived from
// token claims or userinfo (contract §2.7).
type Profile struct {
	Subject     string
	DisplayName string
	AvatarURL   string
}

// ProfileFromClaims maps claims to a Profile using the contract §2.7 fallback
// rules: display_name from name -> preferred_username -> sub; avatar_url from
// picture (empty when absent, no local avatar storage).
func ProfileFromClaims(c *Claims) Profile {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = strings.TrimSpace(c.PreferredUsername)
	}
	if name == "" {
		name = c.Subject
	}
	return Profile{
		Subject:     c.Subject,
		DisplayName: name,
		AvatarURL:   strings.TrimSpace(c.Picture),
	}
}

// ToDTO converts a store.User into a wire protocol.User, injecting the computed
// is_owner field from the OwnerSet (contract §5.3). Times are serialised as
// RFC3339 UTC (contract §7.4).
func ToDTO(u store.User, owners *OwnerSet) protocol.User {
	return protocol.User{
		ID:           u.ID,
		OAuthSubject: u.OAuthSubject,
		DisplayName:  u.DisplayName,
		AvatarURL:    u.AvatarURL,
		IsOwner:      owners.IsOwner(u.OAuthSubject),
		CreatedAt:    u.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    u.UpdatedAt.UTC().Format(time.RFC3339),
	}
}
