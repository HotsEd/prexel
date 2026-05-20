package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

// Connect dials host:port and performs the SSH handshake using signer as the
// only authentication method. The provided hostkey callback decides whether the
// server's host key is acceptable (see Callback for the TOFU implementation).
//
// The dial honours ctx for cancellation/deadlines; if ctx has no deadline a
// fallback timeout is used.
func Connect(ctx context.Context, host string, port int, user string, signer ssh.Signer, hostkey ssh.HostKeyCallback, timeout time.Duration) (*ssh.Client, error) {
	if host == "" {
		return nil, errors.New("ssh: empty host")
	}
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("ssh: invalid port %d", port)
	}
	if user == "" {
		return nil, errors.New("ssh: empty user")
	}
	if signer == nil {
		return nil, errors.New("ssh: nil signer")
	}
	if hostkey == nil {
		return nil, errors.New("ssh: nil host key callback")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostkey,
		Timeout:         timeout,
	}

	dialer := &net.Dialer{Timeout: timeout}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh: tcp dial %s: %w", addr, err)
	}

	// Apply context deadline to the handshake, if any.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh: handshake %s: %w", addr, err)
	}
	// Clear deadline so the live session isn't killed mid-command.
	_ = conn.SetDeadline(time.Time{})

	return ssh.NewClient(c, chans, reqs), nil
}

// RunCommand executes cmd over a fresh session on client and returns the
// captured stdout and stderr, plus any session error.
func RunCommand(client *ssh.Client, cmd string) (stdout, stderr string, err error) {
	if client == nil {
		return "", "", errors.New("ssh: nil client")
	}
	sess, err := client.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("ssh: new session: %w", err)
	}
	defer func() { _ = sess.Close() }()

	var outBuf, errBuf bytes.Buffer
	sess.Stdout = &outBuf
	sess.Stderr = &errBuf
	runErr := sess.Run(cmd)
	return outBuf.String(), errBuf.String(), runErr
}
