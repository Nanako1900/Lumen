package broker

import (
	"strings"
	"testing"
)

// TestIsLoopbackRedirectURI mirrors loopback.test.ts.
func TestIsLoopbackRedirectURI(t *testing.T) {
	accept := []string{
		"http://127.0.0.1:8931/cb",
		"http://127.0.0.1:1/callback",
		"http://127.0.0.1:65535/x",
	}
	for _, v := range accept {
		if !isLoopbackRedirectURI(v) {
			t.Errorf("isLoopbackRedirectURI(%q) = false, want true", v)
		}
	}

	reject := []string{
		"http://localhost:8931/cb",   // DNS-rebinding defense
		"https://127.0.0.1:8931/cb",  // non-http scheme
		"http://192.168.1.5:8931/cb", // non-loopback host
		"http://example.com/cb",
		"http://0.0.0.0:8931/cb",
		"http://[::1]:8931/cb", // IPv6 loopback not accepted by 127.0.0.1 rule
		"",
		"/cb",               // relative
		"127.0.0.1:8931/cb", // no scheme
		"not a url",
	}
	for _, v := range reject {
		if isLoopbackRedirectURI(v) {
			t.Errorf("isLoopbackRedirectURI(%q) = true, want false", v)
		}
	}
}

// TestIsLoopbackRedirectURILengthCap enforces decision 7's length cap.
func TestIsLoopbackRedirectURILengthCap(t *testing.T) {
	long := "http://127.0.0.1:8931/" + strings.Repeat("a", maxRedirectURILen)
	if isLoopbackRedirectURI(long) {
		t.Error("over-length loopback URI should be rejected")
	}
}

// TestSafeURL mirrors loopback.test.ts safeUrl.
func TestSafeURL(t *testing.T) {
	u := safeURL("http://127.0.0.1:8931/cb")
	if u == nil || u.Hostname() != "127.0.0.1" {
		t.Errorf("safeURL valid input = %v", u)
	}
	if safeURL("garbage") != nil {
		t.Error("safeURL(garbage) should be nil")
	}
	if safeURL("") != nil {
		t.Error("safeURL(empty) should be nil")
	}
}
