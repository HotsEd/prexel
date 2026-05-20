package ssh

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
)

// ErrHostKeyMismatch is returned by the host-key callback when the captured
// fingerprint differs from the one we have stored for a server (Tech Review §3).
var ErrHostKeyMismatch = errors.New("ssh: host key fingerprint mismatch")

// Callback returns an ssh.HostKeyCallback that implements TOFU: on first
// contact (stored == "") it persists the SHA-256 fingerprint via save() and
// accepts the connection; on subsequent connections it requires an exact match.
//
// The save() callback is invoked synchronously inside the SSH handshake. It
// MUST be cheap and side-effect-safe; the caller is responsible for any
// persistence (typically writing to the servers.host_key_fingerprint column).
func Callback(stored string, save func(fp string) error) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		if stored == "" {
			if err := save(fp); err != nil {
				return fmt.Errorf("ssh: persist host key fingerprint: %w", err)
			}
			return nil
		}
		if stored == fp {
			return nil
		}
		return fmt.Errorf("%w: have %s, got %s", ErrHostKeyMismatch, stored, fp)
	}
}
