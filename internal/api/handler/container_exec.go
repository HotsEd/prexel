// Package handler — container exec.
//
// This file implements the interactive terminal endpoint:
//
//	GET /api/v1/apps/{id}/containers/{name}/exec  (WebSocket upgrade)
//
// It is the moral equivalent of `docker exec -it <container> sh` exposed
// to operators through the Prexel web UI (and the `prexel exec` CLI).
//
// SECURITY NOTE — this endpoint is intentionally MORE restrictive than
// the container-logs endpoint:
//
//   - RBAC: apps.update (not apps.view). A shell inside a running
//     container is destructive: an operator can rm -rf the writable
//     layer, edit configuration on disk, exfiltrate secrets, etc. So we
//     require the same privilege as "edit this app".
//   - Ownership: we always verify the {name} container carries the
//     `prexel.app_id=<id>` label before opening the exec. Without that
//     check, any caller with apps.update on ANY team could open a shell
//     in any container by guessing names.
//
// PROTOCOL — the WebSocket wire format is a deliberate hybrid:
//
//   - Binary frames carry raw TTY bytes in both directions. Client sends
//     keystrokes verbatim (the TTY layer handles line discipline,
//     escapes, etc.); server forwards the merged stdout/stderr the same
//     way. This is the simplest possible terminal protocol and avoids
//     base64 or JSON-wrapping overhead.
//   - Text frames carry control messages as small JSON objects. Today
//     the only one is {"type":"resize","cols":N,"rows":N} but the field
//     gives us room to add e.g. {"type":"ping"} or signal injection
//     later without breaking clients.
//
// AUTH — browsers cannot set custom headers on the WebSocket constructor
// (RFC 6455 limitation), so we accept the access token from either:
//
//   - the standard `Authorization: Bearer …` header (CLI path), or
//   - the `?token=<jwt>` query parameter (browser path).
//
// The query-param token is validated the same way as the header path
// (HS256, "sub" claim → user_id). It never persists anywhere — it lives
// only on the upgrade request URL, which terminates inside this handler.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	dockerfilters "github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/go-chi/chi/v5"

	"github.com/coder/websocket"

	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/apitoken"
	"github.com/prexel/prexel/internal/auth"
	"github.com/prexel/prexel/internal/rbac"
	"github.com/prexel/prexel/internal/server"
)

// ContainerExecHandler exposes the WebSocket exec endpoint.
//
// All four dependencies are required; the handler panics on construction
// otherwise (fail-closed: a missing RBAC service must never produce an
// anonymous shell).
type ContainerExecHandler struct {
	apps      *app.Service
	servers   *server.Service
	access    *rbac.Service
	jwtSecret string
	tokens    ExecTokenAuthenticator // optional; nil disables PAT auth
	// devMode toggles the WebSocket origin policy:
	//   - true: explicit allowlist of localhost-style origins (Vite
	//     dev server). InsecureSkipVerify is NEVER used; the policy
	//     is still enforced via OriginPatterns.
	//   - false: derive the expected origin from instanceURL so the
	//     same-origin guarantee is reflexive even when the operator
	//     fronts the API with a CDN that rewrites Host.
	devMode bool
	// instanceURL is the public URL the SPA is served from (e.g.
	// https://prexel.example.com). Used in production to populate
	// OriginPatterns for the WebSocket upgrade. Empty == fall back
	// to the default same-origin check the library performs.
	instanceURL string
}

// ExecTokenAuthenticator narrows the apitoken.Service surface this
// handler uses. Kept structurally identical to
// apimiddleware.TokenAuthenticator so the same *apitoken.Service
// satisfies both.
type ExecTokenAuthenticator interface {
	Authenticate(ctx context.Context, raw string) (userID string, err error)
}

// NewContainerExecHandler wires the handler. `tokens` may be nil; when
// non-nil it enables Personal API Token auth via Authorization header
// (matching the Auth middleware). The JWT secret must always be set —
// the browser path relies on it.
func NewContainerExecHandler(
	apps *app.Service,
	servers *server.Service,
	access *rbac.Service,
	jwtSecret string,
	tokens ExecTokenAuthenticator,
) *ContainerExecHandler {
	if apps == nil {
		panic("handler.NewContainerExecHandler: app service is required")
	}
	if servers == nil {
		panic("handler.NewContainerExecHandler: server service is required")
	}
	if access == nil {
		panic("handler.NewContainerExecHandler: rbac service is required (no anonymous fallback)")
	}
	if strings.TrimSpace(jwtSecret) == "" {
		panic("handler.NewContainerExecHandler: jwt secret is required")
	}
	return &ContainerExecHandler{
		apps:      apps,
		servers:   servers,
		access:    access,
		jwtSecret: jwtSecret,
		tokens:    tokens,
	}
}

// WithDevMode toggles development-mode behaviour for this handler.
// Currently the only difference is the WebSocket origin policy: in
// dev we accept any localhost-style origin (Vite proxy on 5173, the
// raw API on 3000, IPv6 forms) via an OriginPatterns allowlist —
// NEVER via InsecureSkipVerify. Returns the receiver so router
// wiring can chain: `NewContainerExecHandler(...).WithDevMode(true)`.
func (h *ContainerExecHandler) WithDevMode(dev bool) *ContainerExecHandler {
	h.devMode = dev
	return h
}

// WithInstanceURL pins the production origin for the WebSocket upgrade.
// Callers should pass the operator-configured public URL of the
// instance (Cfg.InstanceURL). When empty, production falls back to the
// library default which compares against the request Host — that works
// for direct hits but is brittle when a CDN or proxy rewrites Host.
// Returns the receiver for chained wiring.
func (h *ContainerExecHandler) WithInstanceURL(url string) *ContainerExecHandler {
	h.instanceURL = strings.TrimSpace(url)
	return h
}

// resizeMsg is the JSON shape clients send to forward TTY resize events.
// Fields are pointers so the decoder can detect "missing" vs "zero",
// even though zero is rejected by the resize call anyway.
type resizeMsg struct {
	Type string `json:"type"`
	Cols uint   `json:"cols"`
	Rows uint   `json:"rows"`
}

// Exec handles GET /api/v1/apps/{id}/containers/{name}/exec.
//
// This route bypasses the router's Auth middleware on purpose — browsers
// can't send Authorization on a WS handshake, so we resolve the user_id
// in-handler via either header or ?token= query param. CLI clients keep
// using the header path.
//
// Flow:
//  1. Authenticate (header or query) → user_id.
//  2. Resolve app + RBAC (apps.update) on its team.
//  3. Resolve Docker provider for the app's server.
//  4. Verify the named container belongs to this app via label.
//  5. Upgrade the connection to WebSocket.
//  6. Create the exec (TTY=true), attach, and start the bidirectional
//     proxy. Either side closing terminates the other.
func (h *ContainerExecHandler) Exec(w http.ResponseWriter, r *http.Request) {
	// 1. Authenticate. We can't sit behind apimiddleware.Auth because
	//    browsers can't send Authorization on the WebSocket upgrade.
	//    `matchedSubprotocol` is non-empty when auth came via the
	//    `bearer.<jwt>` Sec-WebSocket-Protocol header — we must echo it
	//    back below so the browser handshake completes.
	userID, matchedSubprotocol, err := h.authenticate(r)
	if err != nil {
		// Match the middleware's plain-text 401 so the CLI's
		// IsAuthError() still triggers.
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	appID := chi.URLParam(r, "id")
	containerName := strings.TrimSpace(chi.URLParam(r, "name"))
	if containerName == "" {
		writeError(w, http.StatusBadRequest, "missing_container_name")
		return
	}

	// 2. Resolve the app + apps.update RBAC check.
	a, err := h.apps.Get(r.Context(), appID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	allowed, err := h.access.CanAccessTeam(r.Context(), userID, a.TeamID, rbac.PermAppsUpdate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if a.ServerID == nil || strings.TrimSpace(*a.ServerID) == "" {
		writeError(w, http.StatusBadRequest, "no_server")
		return
	}

	// 3. Docker provider — owned by this handler call, closed on return.
	provider, err := h.servers.Provider(r.Context(), *a.ServerID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "provider_unavailable")
		return
	}
	defer func() { _ = provider.Close() }()

	cli, err := provider.Client(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "provider_unavailable")
		return
	}

	// 4. Ownership check: the named container MUST carry our app label.
	//    Same 403-on-miss policy as container_logs.go: do not leak
	//    existence of containers in other teams.
	//
	//    Pass `a.ID` (canonical app id from the DB) instead of the raw
	//    URL param — Service.Get accepts either id or name, so if the
	//    URL ever carries a name the filter must use the resolved UUID
	//    that actually matches the container's `prexel.app_id` label.
	if !execContainerBelongsToApp(r.Context(), cli, a.ID, containerName) {
		slog.Warn("exec: container ownership check failed",
			"app_id", a.ID,
			"container", containerName,
		)
		writeError(w, http.StatusForbidden, "container_not_found")
		return
	}

	// 5. Default shell. Operators can override with `?cmd=bash` etc. We
	//    fall back to `/bin/sh` because it exists in alpine, debian-slim,
	//    ubuntu, and most distroless+shell images. Pure-distroless will
	//    fail with "exec: no such file or directory" — caller decides.
	shell := strings.TrimSpace(r.URL.Query().Get("cmd"))
	if shell == "" {
		shell = "/bin/sh"
	}

	// 6. Upgrade. Origin policy:
	//
	//    - Development: explicit allowlist of localhost-style origins
	//      (any port). The Vite dev server sends Origin: http://localhost:5173
	//      while the proxy rewrites Host to prexel:3000 — without an
	//      OriginPatterns entry the default same-origin check would
	//      reject the upgrade with 403. We DO NOT use
	//      InsecureSkipVerify here (it disables the check entirely and
	//      would accept any cross-site WS handshake — a real CSRF/clickjack
	//      vector). The JWT query auth remains the gate.
	//
	//    - Production: derive the expected origin from the
	//      operator-configured InstanceURL so the same-origin guarantee
	//      survives proxies/CDNs that rewrite Host. Falling back to the
	//      library default when InstanceURL is empty keeps tests and
	//      bare-bones setups working.
	acceptOpts := &websocket.AcceptOptions{
		OriginPatterns: h.originPatterns(),
	}
	// Echo the matched subprotocol back so the browser handshake
	// completes (RFC 6455 §1.3). Empty when auth came via header or
	// query — leaving Subprotocols nil tells coder/websocket to skip
	// the protocol negotiation entirely.
	if matchedSubprotocol != "" {
		acceptOpts.Subprotocols = []string{matchedSubprotocol}
	}
	wsConn, err := websocket.Accept(w, r, acceptOpts)
	if err != nil {
		// Accept already wrote a response on failure.
		return
	}
	// Large enough for a paste of a long line; the read side is the
	// keystroke channel which is naturally tiny per message.
	wsConn.SetReadLimit(32 << 10)
	defer func() { _ = wsConn.CloseNow() }()

	// All the work below runs on a context derived from the WS lifetime
	// so a client disconnect tears down the docker side immediately.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if err := h.runExec(ctx, wsConn, cli, containerName, shell); err != nil {
		// Best-effort: signal the close reason. Most clients only see
		// the close code, not the reason — that's fine.
		_ = wsConn.Close(websocket.StatusInternalError, truncateReason(err.Error()))
		return
	}
	_ = wsConn.Close(websocket.StatusNormalClosure, "")
}

// runExec is the bidirectional proxy between the WebSocket and the
// hijacked Docker exec connection.
//
// We split into two goroutines:
//
//   - WS → docker: read client frames (binary = stdin bytes, text =
//     control JSON like resize) and write the bytes to the hijacked
//     net.Conn.
//   - docker → WS: read bytes from the hijacked reader and forward as
//     binary WS frames.
//
// Either side returning closes the parent ctx, which causes the other
// side to unwind too.
func (h *ContainerExecHandler) runExec(
	ctx context.Context,
	wsConn *websocket.Conn,
	cli *client.Client,
	containerName, shell string,
) error {
	// Create the exec with TTY mode — single muxed stream (stdout +
	// stderr merged), which is what a real terminal wants.
	idResp, err := cli.ContainerExecCreate(ctx, containerName, dockercontainer.ExecOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Cmd:          []string{shell},
	})
	if err != nil {
		return err
	}

	// Attach. The returned HijackedResponse holds a hijacked net.Conn —
	// we own it from here and must Close() it on the way out.
	hijack, err := cli.ContainerExecAttach(ctx, idResp.ID, dockercontainer.ExecAttachOptions{
		Tty: true,
	})
	if err != nil {
		return err
	}
	defer hijack.Close()

	// Best-effort initial resize so the first prompt renders sanely.
	// If the client sent ?cols= and ?rows= as query params we use them;
	// otherwise we leave the default Docker picks (usually 80x24).
	if cols, rows, ok := initialSize(ctx); ok {
		_ = cli.ContainerExecResize(ctx, idResp.ID, dockercontainer.ResizeOptions{
			Height: rows,
			Width:  cols,
		})
	}

	// downstream errors flow through this channel; first writer wins.
	errCh := make(chan error, 2)

	// docker → WS: read raw TTY bytes and forward as binary frames.
	// The hijacked Reader is a *bufio.Reader; small buffer + frequent
	// flush keeps latency low (terminal feel).
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := hijack.Reader.Read(buf)
			if n > 0 {
				if werr := wsConn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					errCh <- werr
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					errCh <- nil
				} else {
					errCh <- err
				}
				return
			}
		}
	}()

	// WS → docker: read frames, dispatch by message type.
	go func() {
		for {
			mt, data, err := wsConn.Read(ctx)
			if err != nil {
				// Normal close or context cancel — surface as nil so
				// the parent treats it as a clean shutdown.
				errCh <- nil
				return
			}
			switch mt {
			case websocket.MessageBinary:
				// Raw keystrokes. Forward verbatim to the exec
				// stdin. Hijacked conn is a net.Conn — Write blocks
				// until the byte goes out (no buffering layer).
				if _, werr := hijack.Conn.Write(data); werr != nil {
					errCh <- werr
					return
				}
			case websocket.MessageText:
				// Control message. Today: only resize. We swallow
				// unknown types silently so future clients can add
				// fields without breaking older servers.
				var msg resizeMsg
				if err := json.Unmarshal(data, &msg); err != nil {
					// Bad control frame — drop it; the user
					// experience is "my resize was a no-op", not
					// "my session died". The compromise is
					// intentional.
					continue
				}
				if msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
					_ = cli.ContainerExecResize(ctx, idResp.ID, dockercontainer.ResizeOptions{
						Height: msg.Rows,
						Width:  msg.Cols,
					})
				}
			}
		}
	}()

	// First side to return wins. We then cancel the ctx so the other
	// side unwinds, and surface whatever error we got.
	err = <-errCh
	// Give the second goroutine a moment to drain — purely cosmetic
	// (the deferred Close()s would unblock it anyway).
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-errCh:
	case <-timer.C:
	}
	return err
}

// authenticate resolves a user_id from one of three transports:
//
//  1. `Authorization: Bearer <jwt|pat>` header (CLI path).
//  2. `Sec-WebSocket-Protocol: bearer.<jwt>` subprotocol header
//     (preferred browser path — the token never leaves the upgrade
//     request, so it cannot leak via proxy access logs, browser
//     history, or Referer headers the way `?token=` does).
//  3. `?token=<jwt>` query parameter (legacy browser fallback,
//     scheduled for removal once all clients adopt subprotocol auth).
//
// The second return value is the subprotocol token the server must
// echo back via `AcceptOptions.Subprotocols` — non-empty only for the
// subprotocol path. PATs are accepted from the header only; they must
// never end up in a URL or a WS subprotocol.
func (h *ContainerExecHandler) authenticate(r *http.Request) (userID string, matchedSubprotocol string, err error) {
	// Header path (CLI). Same logic as apimiddleware.Auth but inlined
	// because we cannot wrap the WebSocket route with the middleware
	// (browser-side paths need to coexist).
	authz := r.Header.Get("Authorization")
	if strings.HasPrefix(authz, "Bearer ") {
		raw := strings.TrimPrefix(authz, "Bearer ")
		if h.tokens != nil && strings.HasPrefix(raw, apitoken.TokenPrefix) {
			uid, tErr := h.tokens.Authenticate(r.Context(), raw)
			if tErr != nil {
				return "", "", tErr
			}
			return uid, "", nil
		}
		uid, pErr := parseJWTSub(raw, h.jwtSecret)
		return uid, "", pErr
	}

	// Subprotocol path (browser, preferred). The browser sends one or
	// more `Sec-WebSocket-Protocol` values, each a comma-separated list.
	// We look for the first entry that starts with `bearer.` and treat
	// the rest as the raw JWT. If validation succeeds we MUST echo the
	// same protocol token back via AcceptOptions.Subprotocols, otherwise
	// the browser closes the connection (per RFC 6455 §1.3).
	for _, raw := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if !strings.HasPrefix(p, "bearer.") {
				continue
			}
			tok := strings.TrimPrefix(p, "bearer.")
			if tok == "" {
				continue
			}
			uid, pErr := parseJWTSub(tok, h.jwtSecret)
			if pErr != nil {
				return "", "", pErr
			}
			return uid, p, nil
		}
	}

	// Query-param path (legacy browser). Will be removed once all
	// clients have migrated to the subprotocol form. chi/middleware
	// loggers print the path BEFORE the handler runs, so the token can
	// leak into our own access log if anyone wires verbose URL logging
	// — that's the reason we want this branch gone.
	if tok := r.URL.Query().Get("token"); tok != "" {
		uid, pErr := parseJWTSub(tok, h.jwtSecret)
		return uid, "", pErr
	}

	return "", "", errors.New("no credentials")
}

// parseJWTSub validates the JWT signature (HS256), enforces the iss
// claim, and returns the sub claim as the user_id. Delegates to
// auth.ParseAccessToken so the iss/exp/alg checks all live in one
// place.
func parseJWTSub(raw, secret string) (string, error) {
	return auth.ParseAccessToken(secret, raw)
}

// originPatterns returns the allowlist passed to websocket.Accept. See
// the inline comment in Exec for the policy rationale; this function
// exists so the test surface (and any future endpoint that needs the
// same shape) can be exercised in isolation.
//
// We never return a nil-but-empty slice with InsecureSkipVerify set
// somewhere — the InsecureSkipVerify field is deliberately not used
// anywhere in this package.
func (h *ContainerExecHandler) originPatterns() []string {
	if h.devMode {
		// Any port on the standard loopback addresses. coder/websocket
		// matches OriginPatterns against the host:port portion of the
		// Origin header, so "*" covers Vite's :5173, Storybook's :6006,
		// raw API :3000, and anything else a developer might wire up.
		return []string{
			"localhost",
			"localhost:*",
			"127.0.0.1",
			"127.0.0.1:*",
			"[::1]",
			"[::1]:*",
		}
	}
	// Production: derive from InstanceURL. We feed both the bare host
	// and host:port so an operator who pinned the URL to a non-standard
	// port still matches (the library compares ports too).
	if h.instanceURL == "" {
		return nil // fall back to same-origin default
	}
	u, err := url.Parse(h.instanceURL)
	if err != nil || u.Host == "" {
		return nil
	}
	return []string{u.Host}
}

// execContainerBelongsToApp mirrors containerBelongsToApp in
// container_logs.go but takes a *client.Client directly (we have one
// already for the resize call) and a plain context (no *http.Request).
func execContainerBelongsToApp(ctx context.Context, cli *client.Client, appID, name string) bool {
	f := dockerfilters.NewArgs()
	f.Add("label", "prexel.app_id="+appID)
	list, err := cli.ContainerList(ctx, dockercontainer.ListOptions{All: true, Filters: f})
	if err != nil {
		return false
	}
	for _, c := range list {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == name {
				return true
			}
		}
	}
	return false
}

// initialSize is a hook for ?cols=&rows= on the upgrade URL. We could
// extend the URL but today the front-end sends a resize frame
// immediately after connecting anyway — keeping this returning false
// is fine. The function exists so the resize-on-connect call site
// stays clean.
func initialSize(_ context.Context) (cols, rows uint, ok bool) {
	return 0, 0, false
}

// truncateReason caps WebSocket close reasons to the 123-byte limit
// imposed by RFC 6455 §5.5.1. Long Docker error strings (multi-line
// stack traces) would otherwise blow up the close frame.
func truncateReason(s string) string {
	const max = 100 // leave headroom for the code
	if len(s) <= max {
		return s
	}
	return s[:max]
}
