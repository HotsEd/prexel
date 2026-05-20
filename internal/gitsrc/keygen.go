// Package git holds helpers for cloning git repositories using one of the
// three authentication modes supported by Prexel: GitHub App installation
// tokens, SSH deploy keys, and personal access tokens.
package gitsrc

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// GeneratedKey holds a freshly generated Ed25519 keypair encoded for storage
// and presentation. PrivatePEM is OpenSSH-formatted PEM; PublicAuthorized is
// the single-line authorized_keys format suitable for pasting into a deploy
// keys field.
type GeneratedKey struct {
	PrivatePEM       []byte
	PublicAuthorized string
}

// GenerateEd25519 produces a new Ed25519 keypair. It is intentionally
// duplicated from `internal/ssh` (which is being landed by Milestone A4 in
// parallel) so this milestone has no inter-package dependency on A4.
func GenerateEd25519() (*GeneratedKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ed25519: %w", err)
	}

	// MarshalPrivateKey returns a *pem.Block in OpenSSH format which `git`
	// can consume directly via GIT_SSH_COMMAND="ssh -i ...".
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, fmt.Errorf("marshal private: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("ssh public: %w", err)
	}
	authorized := ssh.MarshalAuthorizedKey(sshPub) // already has trailing newline

	return &GeneratedKey{
		PrivatePEM:       pem.EncodeToMemory(block),
		PublicAuthorized: string(authorized),
	}, nil
}
