package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/prexel/prexel/internal/cli/client"
	"github.com/prexel/prexel/internal/gitsrc"
)

/*
   CLI-driven GitHub App registration.

   Rationale (CLAUDE.md, non-negotiable):
   the CLI MUST NOT consume the Prexel web UI to complete an
   operation. The previous "just open <instance>/git" stub violated
   that rule. This flow makes the CLI a self-contained OAuth client:

     1. CLI binds a free localhost:PORT and starts an HTTP server.
     2. CLI generates a GitHub App manifest with redirect_url + setup_url
        pointing at localhost:PORT.
     3. CLI opens the browser straight on localhost:PORT/start, which
        auto-submits the manifest form to GitHub.
     4. GitHub renders "Create App" → user clicks Create.
     5. GitHub redirects to localhost:PORT/callback?code=X.
     6. CLI exchanges the code for App credentials by calling
        api.github.com/app-manifests/{code}/conversions directly (no
        Prexel hop).
     7. CLI posts the resolved credentials to the Prexel API at
        POST /git-sources/github/import (authenticated via the
        operator's existing JWT/PAT). Prexel creates the pending row
        and returns { source_id, install_url }.
     8. CLI redirects the browser to install_url.
     9. GitHub install → redirects to localhost:PORT/install-callback
        ?installation_id=Y.
    10. CLI calls POST /git-sources/{source_id}/finalize, which makes
        Prexel resolve account_login/account_type and unlock the row.
    11. CLI serves a tiny "Pode fechar essa janela" HTML and exits
        the main process with success.

   The only UI the user ever sees is:
     - GitHub's own create/install pages
     - One static localhost HTML at the end saying "you can close this"

   Zero Prexel web UI is touched.
*/

// runLocalGitHubAppFlow orchestrates the whole dance. Blocks until
// the source is finalised or the global timeout fires.
//
// `scope` is "personal" or "org". `org` is the GitHub org slug (empty
// when scope=personal). `nameHint` becomes the source name on the
// Prexel side; the App's GitHub name auto-suffixes from it.
func runLocalGitHubAppFlow(ctx context.Context, api *client.Client, scope, org, nameHint string, log func(format string, args ...any)) (*gitSourceDTO, error) {
	// 1. Bind a free port. Letting the kernel pick (port 0) avoids
	//    races with anything else the user is running locally.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind localhost: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	log("Servidor local: %s", baseURL)

	// 2. Generate the manifest. Manifest's `name` is unique-globally
	//    on GitHub, so we suffix with timestamp to avoid colliding
	//    with previous attempts from the same user.
	manifestName := buildAppName(scope, org, nameHint)
	manifestJSON, err := buildManifest(baseURL, manifestName)
	if err != nil {
		return nil, err
	}
	postAction := manifestPostAction(org)

	// 3. Wire the handlers + result channels. The flow has two
	//    one-shot signals (code, then installation_id) and one
	//    terminal signal (done). Buffered channels keep the HTTP
	//    handlers from blocking if the main goroutine is busy
	//    between steps.
	type flowResult struct {
		source *gitSourceDTO
		err    error
	}
	done := make(chan flowResult, 1)
	state := newOAuthState()

	// sourceID is set by /callback (after Prexel returns it from
	// /import) and consumed by /install-callback (to finalise the
	// right row). The redirect itself happens inside /callback
	// before we cross goroutine boundaries, so install_url stays
	// scoped to that handler.
	var (
		mu       sync.Mutex
		sourceID string
	)

	mux := http.NewServeMux()

	// /start renders an auto-submit form pointing at GitHub.
	mux.HandleFunc("/start", func(w http.ResponseWriter, _ *http.Request) {
		writeHTML(w, manifestFormHTMLLocal(postAction, state, manifestJSON))
	})

	// /callback is where GitHub lands after the user creates the App.
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		gotState := r.URL.Query().Get("state")
		if gotState != state {
			writeHTML(w, simpleErrorHTML("Estado inválido (CSRF guard). Tente de novo."))
			done <- flowResult{err: errors.New("github callback: state mismatch")}
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			writeHTML(w, simpleErrorHTML("GitHub não devolveu o código."))
			done <- flowResult{err: errors.New("github callback: missing code")}
			return
		}
		log("Convertendo manifest…")
		cfg, err := gitsrc.ConvertManifestCode(r.Context(), code)
		if err != nil {
			writeHTML(w, simpleErrorHTML("Falha ao converter o manifest do GitHub."))
			done <- flowResult{err: fmt.Errorf("convert manifest: %w", err)}
			return
		}
		log("Registrando integração no Prexel…")
		var imported struct {
			SourceID   string `json:"source_id"`
			InstallURL string `json:"install_url"`
		}
		body := map[string]string{
			"app_id":      cfg.AppID,
			"slug":        cfg.Slug,
			"private_key": cfg.PrivateKey,
			"name":        nameHint,
		}
		if err := api.Post(r.Context(), "/api/v1/git-sources/github/import", body, &imported); err != nil {
			writeHTML(w, simpleErrorHTML("Prexel rejeitou o import: "+html.EscapeString(err.Error())))
			done <- flowResult{err: fmt.Errorf("prexel import: %w", err)}
			return
		}
		mu.Lock()
		sourceID = imported.SourceID
		mu.Unlock()
		// Redirect the browser straight to GitHub's install page.
		// We don't render any Prexel-ish chrome here.
		http.Redirect(w, r, imported.InstallURL, http.StatusSeeOther)
	})

	// /install-callback is where GitHub lands after the user picks
	// an account/org and clicks Install.
	mux.HandleFunc("/install-callback", func(w http.ResponseWriter, r *http.Request) {
		installationID := r.URL.Query().Get("installation_id")
		if installationID == "" {
			writeHTML(w, simpleErrorHTML("GitHub não devolveu installation_id."))
			done <- flowResult{err: errors.New("install callback: missing installation_id")}
			return
		}
		mu.Lock()
		sid := sourceID
		mu.Unlock()
		if sid == "" {
			writeHTML(w, simpleErrorHTML("Estado interno inconsistente."))
			done <- flowResult{err: errors.New("install callback: source_id not set")}
			return
		}
		log("Finalizando vínculo (installation %s)…", installationID)
		var src gitSourceDTO
		if err := api.Post(r.Context(),
			"/api/v1/git-sources/"+sid+"/finalize",
			map[string]string{"installation_id": installationID},
			&src,
		); err != nil {
			writeHTML(w, simpleErrorHTML("Prexel falhou ao finalizar: "+html.EscapeString(err.Error())))
			done <- flowResult{err: fmt.Errorf("prexel finalize: %w", err)}
			return
		}
		writeHTML(w, simpleSuccessHTML(displayAccount(&src)))
		done <- flowResult{source: &src}
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// 4. Run the server, give it a window to handle the flow, and
	//    shut down whether we succeed, fail, or time out.
	go func() {
		_ = srv.Serve(ln)
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	// 5. Open the browser. If `openBrowser` fails (no GUI on a
	//    headless box), we still print the URL so the operator can
	//    open it manually from another machine on the same host.
	startURL := baseURL + "/start"
	log("Abrindo navegador em %s", startURL)
	if err := openBrowser(startURL); err != nil {
		log("Não consegui abrir o navegador automaticamente. Abra você mesmo:\n  %s", startURL)
	}

	// 6. Wait for either the flow to complete, the context to be
	//    cancelled (Ctrl+C), or our inactivity window to elapse.
	select {
	case res := <-done:
		if res.err != nil {
			return nil, res.err
		}
		return res.source, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Minute):
		return nil, errors.New("timed out waiting for the GitHub flow (5 min)")
	}
}

// ── helpers ────────────────────────────────────────────────────────

// buildAppName produces the human-facing app name for both the
// GitHub-side App registration and the Prexel-side source row. We
// suffix with a short timestamp so retries / new orgs never clash
// with previous names on GitHub (names are GLOBALLY unique there).
func buildAppName(scope, org, nameHint string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "personal"
	}
	hint := strings.TrimSpace(nameHint)
	if hint == "" {
		if org != "" {
			hint = "Prexel · " + org
		} else {
			hint = "Prexel · personal"
		}
	}
	// Short timestamp suffix — 5 last digits of unix seconds give us
	// ~1 day of collision-free retries which is plenty in practice.
	suffix := time.Now().Format("0102-150405")
	return fmt.Sprintf("%s · %s", hint, suffix)
}

// manifestPostAction returns the URL the manifest <form> submits to.
// Personal vs org takes different endpoints on GitHub.
func manifestPostAction(org string) string {
	org = strings.TrimSpace(org)
	if org == "" {
		return "https://github.com/settings/apps/new"
	}
	return "https://github.com/organizations/" + url.PathEscape(org) + "/settings/apps/new"
}

// buildManifest is the GitHub App manifest JSON. Matches the shape
// Prexel's in-panel flow uses (same permissions, same description),
// the only differences are the redirect/setup URLs which point at
// our localhost rather than the panel.
func buildManifest(baseURL, name string) (string, error) {
	manifest := map[string]any{
		"name":            name,
		"url":             "https://github.com/prexel/prexel",
		"description":     "Self-hosted deploys managed by Prexel (CLI install).",
		"redirect_url":    baseURL + "/callback",
		"callback_urls":   []string{baseURL + "/install-callback"},
		"setup_url":       baseURL + "/install-callback",
		"setup_on_update": true,
		"public":          false,
		"default_permissions": map[string]string{
			"contents": "read",
			"metadata": "read",
		},
		"default_events": []string{},
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}
	return string(b), nil
}

// newOAuthState returns a short random string used as the CSRF token
// on the manifest POST. Not security-critical (the whole flow only
// listens on 127.0.0.1) but cheap defence-in-depth.
func newOAuthState() string {
	state, _ := gitsrc.NewManifestState()
	return state
}

// ── HTML helpers (everything served by the local CLI HTTP server) ──

// manifestFormHTMLLocal mirrors gitsrc.ManifestFormHTML but with
// inline action/state — we don't share the panel's because that one
// embeds /api/v1/... URLs of the Prexel base. Kept tiny on purpose;
// "no Prexel UI" means even our localhost page is bare HTML.
func manifestFormHTMLLocal(action, state, manifestJSON string) string {
	actionWithState := action + "?state=" + url.QueryEscape(state)
	return `<!doctype html>
<html><head><meta charset="utf-8"><title>GitHub</title></head>
<body onload="document.getElementById('f').submit()" style="font-family: system-ui, sans-serif; padding: 24px;">
  <p>Redirecionando para o GitHub…</p>
  <form id="f" method="post" action="` + html.EscapeString(actionWithState) + `">
    <input type="hidden" name="manifest" value="` + html.EscapeString(manifestJSON) + `">
    <button type="submit">Continuar manualmente</button>
  </form>
</body></html>`
}

func simpleSuccessHTML(account string) string {
	msg := "GitHub conectado."
	if account != "" {
		msg = "GitHub conectado: " + html.EscapeString(account)
	}
	return `<!doctype html>
<html><head><meta charset="utf-8"><title>OK</title></head>
<body style="font-family: system-ui, sans-serif; padding: 24px;">
  <h1>✓ ` + msg + `</h1>
  <p>Pode fechar essa janela e voltar ao terminal.</p>
  <script>setTimeout(function(){window.close()}, 2000)</script>
</body></html>`
}

func simpleErrorHTML(detail string) string {
	return `<!doctype html>
<html><head><meta charset="utf-8"><title>Erro</title></head>
<body style="font-family: system-ui, sans-serif; padding: 24px;">
  <h1>Algo deu errado</h1>
  <p>` + detail + `</p>
  <p>Você pode fechar essa janela e tentar de novo no terminal.</p>
</body></html>`
}

func writeHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, body)
}

// displayAccount produces "octocat (User)" or just the source name
// when the account info isn't populated yet (shouldn't happen at
// success time, but defence in depth).
func displayAccount(src *gitSourceDTO) string {
	if src == nil {
		return ""
	}
	if src.AccountLogin != "" {
		if src.AccountType != "" {
			return src.AccountLogin + " (" + src.AccountType + ")"
		}
		return src.AccountLogin
	}
	return src.Name
}
