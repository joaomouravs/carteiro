package crypto

import (
	"crypto/rand"
	"encoding/base64"
)

// secretPrefix marks endpoint signing secrets, as the Standard Webhooks
// specification recommends ("whsec_" followed by base64).
const secretPrefix = "whsec_"

// secretSize is within the 24 to 64 bytes the specification allows; 32
// bytes matches the output size of the HMAC-SHA256 that uses it.
const secretSize = 32

// NewEndpointSecret returns a random signing secret.
func NewEndpointSecret() []byte {
	b := make([]byte, secretSize)
	_, _ = rand.Read(b) // never fails since Go 1.24; see NewAPIKey
	return b
}

// FormatEndpointSecret renders a secret the way receivers expect to paste it
// into a Standard Webhooks library.
func FormatEndpointSecret(secret []byte) string {
	return secretPrefix + base64.StdEncoding.EncodeToString(secret)
}
