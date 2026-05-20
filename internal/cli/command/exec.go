// Package command — `prexel exec`.
//
// CLI counterpart of the web UI's interactive terminal. Connects to
//
//	GET /api/v1/apps/{id}/containers/{name}/exec
//
// over WebSocket and proxies the local stdin/stdout to the remote TTY.
//
// Default shell is `/bin/sh` — the same default the server uses when
// no `?cmd=` is sent. Override with --cmd. The handler accepts any
// command path that's executable inside the container.
//
// Local terminal management:
//   - we put stdin into raw mode so the remote shell sees keystrokes
//     verbatim (Ctrl-C, arrows, tab, etc. all forwarded as bytes);
//   - we restore the original tty state on exit, including on SIGINT,
//     so a crash doesn't leave the user's terminal cooked;
//   - we watch SIGWINCH and forward resize events as JSON text frames
//     in the same shape the web UI uses.
package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newExecCmd builds the `prexel exec <app> <container>` command.
func newExecCmd() *cobra.Command {
	var shellCmd string
	c := &cobra.Command{
		Use:   "exec <app> <container>",
		Short: "Open an interactive shell inside a container",
		Long: `Open an interactive shell inside a container belonging to an app.

Requires apps.update permission on the team that owns the app. The
container name must match the prexel.app_id label of the app (use
'prexel app info <app>' to list containers).

The default shell is /bin/sh. Override with --cmd:

  prexel exec myapp myapp-web-1 --cmd bash`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd.Context(), args[0], args[1], shellCmd, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	c.Flags().StringVar(&shellCmd, "cmd", "", "Shell to launch (default: /bin/sh)")
	return c
}

// runExec dials the WS endpoint and runs the proxy loop. Split out from
// the cobra closure so it stays testable in isolation.
func runExec(ctx context.Context, appRef, containerName, shellCmd string, stdout, stderr io.Writer) error {
	c, err := newClient(true)
	if err != nil {
		return err
	}
	a, err := resolveApp(c, ctx, appRef)
	if err != nil {
		return err
	}

	// Build the WS URL. We swap the scheme on the existing base URL so
	// we keep host + port + path + cert pinning all in one place. The
	// server accepts ws/wss; we always upgrade from https/http we know.
	base := c.BaseURL()
	wsURL, err := httpToWS(base)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/api/v1/apps/%s/containers/%s/exec",
		strings.TrimRight(wsURL, "/"), a.ID, url.PathEscape(containerName))
	if shellCmd != "" {
		endpoint += "?cmd=" + url.QueryEscape(shellCmd)
	}

	// Auth — header path. The CLI never uses the ?token= query path
	// (we don't want the token to land in URL access logs on any
	// intermediate proxy).
	hdr := http.Header{}
	if tok := c.Config().AccessToken; tok != "" {
		hdr.Set("Authorization", "Bearer "+tok)
	}

	// We need the cert-pinning callback the CLI client built, but we
	// must NOT inherit its 60s timeout: a WebSocket dial completes
	// fast, but the streaming phase below holds the connection for
	// the lifetime of the shell. Clone the *http.Client without the
	// timeout.
	src := c.HTTPClient()
	httpClient := &http.Client{
		Transport: src.Transport, // shares TLS config + pinning
		Jar:       src.Jar,
	}

	wsConn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPClient: httpClient,
		HTTPHeader: hdr,
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	// Large enough to absorb a single "ls -la" output frame from the
	// remote side. Stdin frames are tiny (one keystroke at a time).
	wsConn.SetReadLimit(1 << 20)
	defer func() { _ = wsConn.CloseNow() }()

	// Stdin / stdout must be a real TTY for raw mode to work. If we're
	// being piped (CI, redirect), we silently fall back to no-raw —
	// the remote side still works as a "send these bytes, dump these
	// bytes" pipe.
	stdinFD := int(os.Stdin.Fd())
	var oldState *term.State
	if term.IsTerminal(stdinFD) {
		oldState, err = term.MakeRaw(stdinFD)
		if err != nil {
			return fmt.Errorf("raw mode: %w", err)
		}
		defer func() { _ = term.Restore(stdinFD, oldState) }()
	}

	// Send initial size + watch SIGWINCH for follow-ups.
	if oldState != nil {
		if cols, rows, err := term.GetSize(stdinFD); err == nil {
			_ = sendResize(ctx, wsConn, uint(cols), uint(rows))
		}
	}
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	// Two-way proxy. Both goroutines write to errCh; first one to
	// finish triggers a context cancel that tears down the other.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 3)

	// remote → local stdout
	go func() {
		for {
			mt, data, err := wsConn.Read(ctx)
			if err != nil {
				// Server-side normal close shows up as a non-EOF
				// error; treat it as clean unless we have other
				// info.
				if errors.Is(err, io.EOF) || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
					errCh <- nil
				} else {
					errCh <- err
				}
				return
			}
			switch mt {
			case websocket.MessageBinary:
				if _, werr := stdout.Write(data); werr != nil {
					errCh <- werr
					return
				}
			case websocket.MessageText:
				// The server doesn't send text frames today; we
				// drop them to keep forward-compat room.
			}
		}
	}()

	// local stdin → remote (binary frames)
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if werr := wsConn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					errCh <- werr
					return
				}
			}
			if err != nil {
				// Local EOF (^D from a pipe) — close the write
				// side cleanly so the remote shell sees EOF too.
				errCh <- nil
				return
			}
		}
	}()

	// SIGWINCH → resize control frame
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-winch:
				if cols, rows, err := term.GetSize(stdinFD); err == nil {
					_ = sendResize(ctx, wsConn, uint(cols), uint(rows))
				}
			}
		}
	}()

	err = <-errCh
	cancel()
	// Drain quickly so deferred Close runs after both goroutines are
	// done. 50ms is plenty; the goroutines exit on ctx cancellation.
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-errCh:
	case <-timer.C:
	}

	_ = wsConn.Close(websocket.StatusNormalClosure, "")
	if err != nil {
		return err
	}
	_ = stderr // reserved for future verbose-mode output
	return nil
}

// sendResize emits the JSON control frame the server expects.
func sendResize(ctx context.Context, c *websocket.Conn, cols, rows uint) error {
	msg := struct {
		Type string `json:"type"`
		Cols uint   `json:"cols"`
		Rows uint   `json:"rows"`
	}{Type: "resize", Cols: cols, Rows: rows}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageText, data)
}

// httpToWS swaps http(s) for ws(s) on a base URL. Anything else
// (already-ws URLs, missing scheme) returns the input unchanged so
// edge cases don't silently break.
func httpToWS(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	return u.String(), nil
}
