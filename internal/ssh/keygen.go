// Package ssh provides SSH primitives used by the Prexel server: Ed25519/RSA
// key parsing, key generation, host-key verification (TOFU with persisted
// SHA-256 fingerprint), and a thin Connect/RunCommand helper.
//
// We intentionally use golang.org/x/crypto/ssh here (not the connhelper-backed
// docker remote provider) because we need fine-grained control over the host
// key callback and the ability to capture the server fingerprint on the first
// connection.
package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// GenerateEd25519 creates a fresh Ed25519 key pair and returns the private key
// PEM-encoded (OpenSSH format) along with the public key in the standard
// "authorized_keys" form ("ssh-ed25519 AAAA…").
func GenerateEd25519() (privatePEM []byte, publicSSH string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("ssh: generate ed25519: %w", err)
	}

	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, "", fmt.Errorf("ssh: marshal private key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(block)

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", fmt.Errorf("ssh: new public key: %w", err)
	}
	authorized := string(ssh.MarshalAuthorizedKey(sshPub))
	return pemBytes, authorized, nil
}

// ParseKey accepts a PEM-encoded private key (PKCS#1, PKCS#8 RSA, or OpenSSH
// Ed25519/RSA) without a passphrase and returns an ssh.Signer for it. v0.1
// rejects passphrase-protected keys explicitly.
func ParseKey(data []byte) (ssh.Signer, error) {
	if len(data) == 0 {
		return nil, errors.New("ssh: empty key")
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		// Surface the underlying error so callers can hint about passphrases.
		return nil, fmt.Errorf("ssh: parse private key: %w", err)
	}
	return signer, nil
}

// PublicKeyToAuthorizedKey serializes the signer's public key in the
// authorized_keys format ("ssh-ed25519 AAAA…", trailing newline trimmed).
func PublicKeyToAuthorizedKey(signer ssh.Signer) string {
	if signer == nil {
		return ""
	}
	out := ssh.MarshalAuthorizedKey(signer.PublicKey())
	// MarshalAuthorizedKey appends "\n"; trim it for storage/display.
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return string(out)
}
