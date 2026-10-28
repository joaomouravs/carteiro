package crypto

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewAPIKey(t *testing.T) {
	k := NewAPIKey()

	if !strings.HasPrefix(k.Key, "crt_") {
		t.Errorf("key %q does not start with crt_", k.Key)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(k.Key, "crt_"))
	if err != nil || len(raw) != 32 {
		t.Errorf("key body must be 32 bytes of base64url, got %d bytes (err %v)", len(raw), err)
	}
	if !strings.HasPrefix(k.Key, k.Prefix) || len(k.Prefix) != 12 {
		t.Errorf("prefix %q is not the first 12 characters of the key", k.Prefix)
	}
	if !bytes.Equal(k.Hash, HashAPIKey(k.Key)) || len(k.Hash) != 32 {
		t.Error("hash is not the SHA-256 of the key")
	}
}

func TestNewAPIKeyIsUnique(t *testing.T) {
	seen := make(map[string]bool)
	for range 1000 {
		k := NewAPIKey()
		if seen[k.Key] {
			t.Fatalf("duplicate key %q", k.Key)
		}
		seen[k.Key] = true
	}
}
