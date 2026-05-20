package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

func TestGenerateEd25519_RoundTrip(t *testing.T) {
	priv, pub, err := GenerateEd25519()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(priv) == 0 || pub == "" {
		t.Fatal("empty key material")
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") {
		t.Errorf("public key prefix wrong: %q", pub)
	}
	signer, err := ParseKey(priv)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if signer == nil {
		t.Fatal("signer nil")
	}
	got := PublicKeyToAuthorizedKey(signer)
	if !strings.HasPrefix(got, "ssh-ed25519 ") {
		t.Errorf("authorized key wrong: %q", got)
	}
}

func TestParseKey_Empty(t *testing.T) {
	if _, err := ParseKey(nil); err == nil {
		t.Error("expected error for nil")
	}
	if _, err := ParseKey([]byte{}); err == nil {
		t.Error("expected error for empty")
	}
}

func TestParseKey_Garbage(t *testing.T) {
	if _, err := ParseKey([]byte("not a key")); err == nil {
		t.Error("expected error for garbage")
	}
}

func TestParseKey_AcceptsRSA(t *testing.T) {
	// Generate a small RSA private key just to confirm the parser accepts both
	// Ed25519 and RSA PEM-encoded keys.
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa gen: %v", err)
	}
	derBytes := x509.MarshalPKCS1PrivateKey(rsaKey)
	pemBlock := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: derBytes}
	pemBytes := pem.EncodeToMemory(pemBlock)

	signer, err := ParseKey(pemBytes)
	if err != nil {
		t.Fatalf("parse RSA: %v", err)
	}
	if signer.PublicKey().Type() != gossh.KeyAlgoRSA {
		t.Errorf("expected RSA key, got %q", signer.PublicKey().Type())
	}
}

func TestPublicKeyToAuthorizedKey_Nil(t *testing.T) {
	if got := PublicKeyToAuthorizedKey(nil); got != "" {
		t.Errorf("expected empty string for nil signer, got %q", got)
	}
}
