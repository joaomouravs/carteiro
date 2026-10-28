// Package crypto holds Carteiro's cryptographic primitives.
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// apiKeyMarker starts every API key. A fixed, distinctive marker lets secret
// scanners (GitHub's, gitleaks) recognize a leaked key in a commit or a log.
const apiKeyMarker = "crt_"

// displayPrefixLen is how much of a key is stored in clear and shown in
// listings: the marker plus 8 random characters (48 bits). That is enough to
// tell a user's keys apart and far too little to guess the remaining 208 bits.
const displayPrefixLen = len(apiKeyMarker) + 8

// APIKey is a newly generated key.
type APIKey struct {
	// Key is the full key. It is shown to the user once and never stored.
	Key string
	// Prefix is the start of Key, safe to store and display.
	Prefix string
	// Hash is what the database stores and looks keys up by.
	Hash []byte
}

// NewAPIKey generates a key with 256 random bits (NFR-SEC-01).
func NewAPIKey() APIKey {
	b := make([]byte, 32)
	// Since Go 1.24, crypto/rand.Read never fails: it always fills b, and the
	// program crashes if the operating system cannot supply randomness.
	_, _ = rand.Read(b)
	key := apiKeyMarker + base64.RawURLEncoding.EncodeToString(b)
	return APIKey{Key: key, Prefix: key[:displayPrefixLen], Hash: HashAPIKey(key)}
}

// HashAPIKey returns the SHA-256 of key. A fast hash is right here, unlike for
// passwords: slow hashes such as bcrypt exist to resist guessing low-entropy
// human secrets, and a 256-bit random key has nothing to guess.
func HashAPIKey(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}
