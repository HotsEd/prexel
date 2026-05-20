package crypto

import (
	"bytes"
	"strings"
	"testing"
)

const testSecret = "test-secret-key-32-chars-minimum-aaaaaa"

func TestNew_RejectsShortSecret(t *testing.T) {
	if _, err := New("short"); err == nil {
		t.Fatal("expected error for short secret, got nil")
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	c, err := New(testSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []string{
		"",
		"hello",
		"the quick brown fox jumps over the lazy dog",
		strings.Repeat("a", 4096),
	}
	for _, pt := range cases {
		ct, err := c.Encrypt([]byte(pt))
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", pt, err)
		}
		got, err := c.Decrypt(ct)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if string(got) != pt {
			t.Errorf("round trip mismatch: want %q got %q", pt, got)
		}
	}
}

func TestDecrypt_RejectsDifferentKey(t *testing.T) {
	c1, _ := New(testSecret)
	c2, _ := New("another-secret-key-32-chars-minimum-bbb")

	ct, err := c1.Encrypt([]byte("payload"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := c2.Decrypt(ct); err == nil {
		t.Error("expected decrypt with different key to fail; got nil")
	}
}

func TestEncrypt_UniqueNonces(t *testing.T) {
	c, _ := New(testSecret)
	a, err := c.Encrypt([]byte("same"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	b, err := c.Encrypt([]byte("same"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Error("two encryptions of same plaintext produced identical ciphertext (nonce reuse?)")
	}
	// First nonceLen bytes are the nonce; verify they differ.
	if bytes.Equal(a[:nonceLen], b[:nonceLen]) {
		t.Error("nonces are equal across two encryptions")
	}
}

func TestDecrypt_RejectsShortData(t *testing.T) {
	c, _ := New(testSecret)
	if _, err := c.Decrypt([]byte("short")); err == nil {
		t.Error("expected error for short ciphertext")
	}
}

func TestDecrypt_RejectsTamperedCiphertext(t *testing.T) {
	c, _ := New(testSecret)
	ct, _ := c.Encrypt([]byte("payload"))
	// Flip one byte in the ciphertext payload.
	tampered := make([]byte, len(ct))
	copy(tampered, ct)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := c.Decrypt(tampered); err == nil {
		t.Error("expected GCM auth failure on tampered ciphertext")
	}
}
