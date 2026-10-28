package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func newTestCipher(t *testing.T, fill byte) *Cipher {
	t.Helper()
	c, err := NewCipher(bytes.Repeat([]byte{fill}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCipherRoundTrip(t *testing.T) {
	c := newTestCipher(t, 1)
	secret, ad := []byte("endpoint signing secret"), []byte("endpoint-id")

	ct := c.Encrypt(secret, ad)
	if bytes.Contains(ct, secret) {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := c.Decrypt(ct, ad)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("Decrypt = %q, %v", got, err)
	}
}

func TestCipherNoncesAreRandom(t *testing.T) {
	c := newTestCipher(t, 1)
	a, b := c.Encrypt([]byte("same"), nil), c.Encrypt([]byte("same"), nil)
	if bytes.Equal(a, b) {
		t.Error("encrypting the same plaintext twice gave the same ciphertext")
	}
}

func TestCipherRejects(t *testing.T) {
	c := newTestCipher(t, 1)
	ct := c.Encrypt([]byte("secret"), []byte("endpoint-a"))

	tampered := bytes.Clone(ct)
	tampered[len(tampered)-1] ^= 1

	tests := []struct {
		name string
		c    *Cipher
		ct   []byte
		ad   []byte
	}{
		{"other record's associated data", c, ct, []byte("endpoint-b")},
		{"tampered ciphertext", c, tampered, []byte("endpoint-a")},
		{"truncated ciphertext", c, ct[:10], []byte("endpoint-a")},
		{"different key", newTestCipher(t, 2), ct, []byte("endpoint-a")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.c.Decrypt(tt.ct, tt.ad); !errors.Is(err, ErrDecrypt) {
				t.Errorf("err = %v, want ErrDecrypt", err)
			}
		})
	}
}

func TestNewCipherKeySize(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33} {
		if _, err := NewCipher(make([]byte, n)); err == nil {
			t.Errorf("NewCipher accepted a %d-byte key", n)
		}
	}
}
