package auth

import "testing"

func TestOwnerSet(t *testing.T) {
	owners := NewOwnerSet([]string{"sub-abc", "sub-def", ""})
	if !owners.IsOwner("sub-abc") {
		t.Error("sub-abc should be owner")
	}
	if !owners.IsOwner("sub-def") {
		t.Error("sub-def should be owner")
	}
	if owners.IsOwner("sub-xyz") {
		t.Error("sub-xyz should not be owner")
	}
	if owners.IsOwner("") {
		t.Error("empty subject should not be owner (dropped at construction)")
	}
}

func TestOwnerSet_Empty(t *testing.T) {
	owners := NewOwnerSet(nil)
	if owners.IsOwner("anyone") {
		t.Error("empty owner set should never report owner")
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header  string
		wantTok string
		wantOK  bool
	}{
		{"Bearer abc.def.ghi", "abc.def.ghi", true},
		{"Bearer   spaced  ", "spaced", true},
		{"bearer lowercase", "", false},
		{"Basic xyz", "", false},
		{"", "", false},
		{"Bearer ", "", false},
	}
	for _, tt := range tests {
		tok, ok := BearerToken(tt.header)
		if ok != tt.wantOK || tok != tt.wantTok {
			t.Errorf("BearerToken(%q) = (%q, %v), want (%q, %v)",
				tt.header, tok, ok, tt.wantTok, tt.wantOK)
		}
	}
}
