// Package crypto implements AES-256-GCM encryption keyed by a derivation of
// the instance secret. The key is derived via HKDF-SHA256 with a fixed info
// label, so rotating PREXEL_SECRET_KEY invalidates all stored ciphertexts.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	keyLen   = 32 // AES-256
	nonceLen = 12 // standard GCM nonce
	hkdfInfo = "prexel-encryption-v1"
)

// Cipher is a reusable AEAD cipher bound to a derived key.
type Cipher struct {
	aead cipher.AEAD
}

// New derives a 32-byte key from secret via HKDF-SHA256 and constructs a GCM
// cipher around AES-256.
func New(secret string) (*Cipher, error) {
	if len(secret) < 32 {
		return nil, errors.New("crypto: secret must be at least 32 bytes")
	}
	key := make([]byte, keyLen)
	r := hkdf.New(sha256.New, []byte(secret), nil, []byte(hkdfInfo))
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("hkdf: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt returns nonce||ciphertext. Output is suitable for storage as BLOB.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	ct := c.aead.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// Decrypt parses nonce||ciphertext and returns plaintext.
func (c *Cipher) Decrypt(data []byte) ([]byte, error) {
	if len(data) < nonceLen+c.aead.Overhead() {
		return nil, errors.New("crypto: ciphertext too short")
	}
	nonce, ct := data[:nonceLen], data[nonceLen:]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	return pt, nil
}
