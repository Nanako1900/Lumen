package auth

import (
	"testing"
	"time"

	"lumen/internal/store"

	"github.com/golang-jwt/jwt/v5"
)

func TestProfileFromClaims_Fallback(t *testing.T) {
	tests := []struct {
		name     string
		claims   Claims
		wantName string
		wantPic  string
	}{
		{
			name: "name present",
			claims: Claims{Name: "Nanako", Picture: "https://cdn/a.png",
				RegisteredClaims: jwt.RegisteredClaims{Subject: "sub-1"}},
			wantName: "Nanako", wantPic: "https://cdn/a.png",
		},
		{
			name: "fallback to preferred_username",
			claims: Claims{PreferredUsername: "nana",
				RegisteredClaims: jwt.RegisteredClaims{Subject: "sub-2"}},
			wantName: "nana", wantPic: "",
		},
		{
			name:     "fallback to sub",
			claims:   Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "sub-3"}},
			wantName: "sub-3", wantPic: "",
		},
		{
			name: "whitespace name falls through",
			claims: Claims{Name: "   ", PreferredUsername: "  user  ",
				RegisteredClaims: jwt.RegisteredClaims{Subject: "sub-4"}},
			wantName: "user", wantPic: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ProfileFromClaims(&tt.claims)
			if p.DisplayName != tt.wantName {
				t.Errorf("DisplayName = %q, want %q", p.DisplayName, tt.wantName)
			}
			if p.AvatarURL != tt.wantPic {
				t.Errorf("AvatarURL = %q, want %q", p.AvatarURL, tt.wantPic)
			}
			if p.Subject != tt.claims.Subject {
				t.Errorf("Subject = %q, want %q", p.Subject, tt.claims.Subject)
			}
		})
	}
}

func TestToDTO_InjectsIsOwnerAndFormatsTime(t *testing.T) {
	owners := NewOwnerSet([]string{"sub-owner"})
	created := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 6, 29, 8, 30, 0, 0, time.UTC)

	ownerDTO := ToDTO(store.User{
		ID: "u1", OAuthSubject: "sub-owner", DisplayName: "Boss",
		CreatedAt: created, UpdatedAt: updated,
	}, owners)
	if !ownerDTO.IsOwner {
		t.Error("expected IsOwner=true for configured owner")
	}
	if ownerDTO.CreatedAt != "2026-06-01T00:00:00Z" {
		t.Errorf("CreatedAt = %q, want RFC3339 UTC", ownerDTO.CreatedAt)
	}
	if ownerDTO.UpdatedAt != "2026-06-29T08:30:00Z" {
		t.Errorf("UpdatedAt = %q, want RFC3339 UTC", ownerDTO.UpdatedAt)
	}

	memberDTO := ToDTO(store.User{ID: "u2", OAuthSubject: "sub-plain"}, owners)
	if memberDTO.IsOwner {
		t.Error("expected IsOwner=false for non-owner")
	}
}

func TestToDTO_LocalTimeConvertedToUTC(t *testing.T) {
	owners := NewOwnerSet(nil)
	loc := time.FixedZone("CST", 8*3600)
	local := time.Date(2026, 6, 29, 16, 30, 0, 0, loc) // 08:30 UTC
	dto := ToDTO(store.User{ID: "u", OAuthSubject: "s", CreatedAt: local, UpdatedAt: local}, owners)
	if dto.CreatedAt != "2026-06-29T08:30:00Z" {
		t.Errorf("CreatedAt = %q, want 2026-06-29T08:30:00Z (UTC normalised)", dto.CreatedAt)
	}
}
