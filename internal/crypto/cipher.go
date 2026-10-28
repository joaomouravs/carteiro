package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
)

// KeySize is the size of an AES-256 key.
const KeySize = 32

// ErrDecrypt means a ciphertext was tampered with, belongs to another record,
// or was encrypted with a different key. The cases are deliberately not told
// apart.
var ErrDecrypt = errors.New("crypto: decryption failed")

// Cipher encrypts secrets at rest with AES-256-GCM (NFR-SEC-02).
//
// Unlike API keys, endpoint secrets cannot be stored as a hash: Carteiro needs
// the secret itself to sign every delivery. Encryption means a leaked
// database dump or backup does not leak the secrets, as long as the key,
// which lives only in the environment, does not leak with it.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher returns a Cipher for a 32-byte key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("crypto: key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	// A fresh random 96-bit nonce per message, prepended to the ciphertext.
	// GCM breaks badly if a nonce repeats under one key; random nonces are
	// safe for far more secrets than Carteiro will ever encrypt.
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals plaintext. associatedData is authenticated but not encrypted:
// passing the owning record's id binds the ciphertext to that record, so
// copying one endpoint's encrypted secret onto another makes it fail to
// decrypt instead of silently working.
func (c *Cipher) Encrypt(plaintext, associatedData []byte) []byte {
	return c.aead.Seal(nil, nil, plaintext, associatedData) //nolint:gosec // G407 false positive: with NewGCMWithRandomNonce the nonce argument must be nil, Seal generates a random one
}

// Decrypt opens a ciphertext produced by Encrypt with the same associatedData.
func (c *Cipher) Decrypt(ciphertext, associatedData []byte) ([]byte, error) {
	plaintext, err := c.aead.Open(nil, nil, ciphertext, associatedData)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}
