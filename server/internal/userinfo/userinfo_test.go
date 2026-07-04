package userinfo

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    Info
		wantErr bool
	}{
		{
			name: "OIDC standard claims",
			body: `{"sub":"abc-123","name":"Alice","picture":"https://x/a.png","email":"a@b.com"}`,
			want: Info{Subject: "abc-123", DisplayName: "Alice", AvatarURL: "https://x/a.png", Email: "a@b.com"},
		},
		{
			name: "Nanako-style plain OAuth2 (id/username/avatar, numeric id)",
			body: `{"id":12345,"username":"nanako","avatar":"https://x/n.png","email":"n@nanako.org"}`,
			want: Info{Subject: "12345", DisplayName: "nanako", AvatarURL: "https://x/n.png", Email: "n@nanako.org"},
		},
		{
			name: "preferred_username fallback for name",
			body: `{"sub":"u1","preferred_username":"bob"}`,
			want: Info{Subject: "u1", DisplayName: "bob"},
		},
		{
			name: "user_id + nickname + avatar_url aliases",
			body: `{"user_id":"u-9","nickname":"carol","avatar_url":"https://x/c.png"}`,
			want: Info{Subject: "u-9", DisplayName: "carol", AvatarURL: "https://x/c.png"},
		},
		{
			name: "nested data envelope",
			body: `{"code":0,"data":{"id":777,"username":"deep","avatar":"https://x/d.png"}}`,
			want: Info{Subject: "777", DisplayName: "deep", AvatarURL: "https://x/d.png"},
		},
		{
			name: "string id preferred over numeric ordering (sub wins)",
			body: `{"sub":"real-sub","id":999}`,
			want: Info{Subject: "real-sub"},
		},
		{
			name: "missing subject yields empty subject, no error",
			body: `{"name":"nobody"}`,
			want: Info{DisplayName: "nobody"},
		},
		{
			name:    "malformed json errors",
			body:    `{not json`,
			wantErr: true,
		},
		{
			name: "whitespace trimmed",
			body: `{"sub":"  s1  ","name":"  Al  "}`,
			want: Info{Subject: "s1", DisplayName: "Al"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (info=%+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Parse() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
