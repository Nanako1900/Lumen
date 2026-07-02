package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// AES-256-GCM parameters. The nonce (IV) is 12 bytes, matching the standard
// GCM nonce size and the EdgeOne session.ts implementation exactly. GCM's
// authentication tag (16 bytes) is appended to the ciphertext by Go's Seal.
const (
	keySize   = 32 // AES-256
	nonceSize = 12
)

// Sealer encrypts and decrypts opaque payloads with a fixed AES-256-GCM key.
// It backs the two host-only sealed cookies (lumen_auth_flow, lumen_session)
// and the at-rest refresh_token encryption; each concern uses its own key
// (LUMEN_SESSION_ENC_KEY vs LUMEN_REFRESH_ENC_KEY) via a distinct Sealer.
//
// A Sealer is immutable and safe for concurrent use.
type Sealer struct {
	aead cipher.AEAD
}

// NewSealer builds a Sealer from a raw 32-byte AES-256 key. It returns an
// error rather than panicking so callers can fail fast at config load.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("secure: key must be %d bytes (AES-256-GCM), got %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secure: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secure: new gcm: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plaintext and returns "base64url(iv).base64url(ciphertext)"
// (mirrors session.ts sealSession). A fresh random 12-byte IV is generated per
// call via crypto/rand.
func (s *Sealer) Seal(plaintext []byte) (string, error) {
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secure: read nonce: %w", err)
	}
	ciphertext := s.aead.Seal(nil, nonce, plaintext, nil)
	return Base64URLEncode(nonce) + "." + Base64URLEncode(ciphertext), nil
}

// Open reverses Seal. Malformed, tampered (GCM auth failure), or otherwise
// invalid input returns an error (mirrors session.ts openSession returning
// null). Callers treat any error as "no valid payload".
func (s *Sealer) Open(sealed string) ([]byte, error) {
	dot := strings.IndexByte(sealed, '.')
	if dot <= 0 || dot >= len(sealed)-1 {
		return nil, fmt.Errorf("secure: malformed sealed value")
	}
	nonce, err := Base64URLDecode(sealed[:dot])
	if err != nil {
		return nil, fmt.Errorf("secure: decode iv: %w", err)
	}
	if len(nonce) != nonceSize {
		return nil, fmt.Errorf("secure: iv must be %d bytes", nonceSize)
	}
	ciphertext, err := Base64URLDecode(sealed[dot+1:])
	if err != nil {
		return nil, fmt.Errorf("secure: decode ciphertext: %w", err)
	}
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("secure: gcm open (tampered or wrong key): %w", err)
	}
	return plaintext, nil
}

// Encrypt seals plaintext into raw bytes ("iv||ciphertext"), suitable for
// storing refresh_token at rest as bytea (decision 2). Unlike Seal it does not
// base64-encode: the DB column is binary. The 12-byte IV is prepended to the
// GCM output.
func (s *Sealer) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secure: read nonce: %w", err)
	}
	// Seal appends ciphertext+tag to the dst; passing nonce as dst prefixes it.
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt, reading the 12-byte IV prefix.
func (s *Sealer) Decrypt(sealed []byte) ([]byte, error) {
	if len(sealed) < nonceSize {
		return nil, fmt.Errorf("secure: ciphertext too short")
	}
	nonce := sealed[:nonceSize]
	ciphertext := sealed[nonceSize:]
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("secure: gcm open (tampered or wrong key): %w", err)
	}
	return plaintext, nil
}

// DecodeKey decodes a base64 (standard or url-safe, padded or not) key string
// and requires it to be exactly 32 bytes (mirrors session.ts decodeKey +
// the 32-byte guard). It is used by config to validate LUMEN_SESSION_ENC_KEY
// and LUMEN_REFRESH_ENC_KEY at load time.
func DecodeKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("secure: empty key")
	}
	// Accept both alphabets and both padding modes, like the TS decoder which
	// normalizes -_ to +/ and re-pads.
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(value); err == nil {
			if len(b) != keySize {
				return nil, fmt.Errorf("secure: key must decode to %d bytes (AES-256-GCM), got %d", keySize, len(b))
			}
			return b, nil
		}
	}
	return nil, fmt.Errorf("secure: key is not valid base64")
}
