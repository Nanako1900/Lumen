package broker

import "net/url"

// maxRedirectURILen caps the desktop loopback redirect_uri length (decision 7).
// The loopback address is always a short http://127.0.0.1:<port>/... URL; a very
// long value is a red flag and is rejected before any parsing/staging.
const maxRedirectURILen = 512

// isLoopbackRedirectURI reports whether value is an acceptable desktop loopback
// redirect_uri (mirrors loopback.ts isLoopbackRedirectUri, plus the length cap
// from decision 7). Only http://127.0.0.1:<port>/... is allowed:
//   - scheme must be http (loopback plaintext is acceptable; any port),
//   - hostname must be the literal 127.0.0.1 (reject "localhost" to defeat DNS
//     rebinding),
//   - relative/malformed URLs are rejected.
func isLoopbackRedirectURI(value string) bool {
	if len(value) == 0 || len(value) > maxRedirectURILen {
		return false
	}
	u := safeURL(value)
	if u == nil {
		return false
	}
	if u.Scheme != "http" {
		return false
	}
	if u.Hostname() != "127.0.0.1" {
		return false
	}
	return true
}

// safeURL parses value as an absolute URL, returning nil on any malformed or
// non-absolute input (mirrors loopback.ts safeUrl, which relies on the WHATWG
// URL constructor rejecting relative URLs). Go's url.Parse is permissive with
// relative refs, so we additionally require a scheme and host.
func safeURL(value string) *url.URL {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil {
		return nil
	}
	if u.Scheme == "" || u.Host == "" {
		return nil
	}
	return u
}
