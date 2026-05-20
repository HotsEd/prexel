package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/gitsrc"
)

// InstanceURLReader is the slice of internal/instance.Service the
// git-source handler needs to compute webhook URLs without taking a
// hard dependency on the full settings service (tests inject a tiny
// fake instead).
type InstanceURLReader interface {
	CurrentInstanceURL() string
}

// GitSourceDeps bundles dependencies used by the git-source endpoints.
type GitSourceDeps struct {
	Repo     *gitsrc.Repo
	App      *gitsrc.GitHubApp
	Instance InstanceURLReader // optional — when nil, webhook_url is omitted
}

// GitSource groups CRUD + GitHub App helpers under /api/v1/git-sources.
type GitSource struct {
	d GitSourceDeps
}

// NewGitSourceHandler wires the handler bundle.
func NewGitSourceHandler(d GitSourceDeps) *GitSource {
	return &GitSource{d: d}
}

// webhookURLFor builds the public callback URL GitHub should POST to.
// Returns "" when no instance URL is configured (panel is in IP mode);
// the UI then renders a "configure an instance URL first" hint.
func (h *GitSource) webhookURLFor(sourceID string) string {
	if h.d.Instance == nil {
		return ""
	}
	host := strings.TrimSpace(h.d.Instance.CurrentInstanceURL())
	if host == "" {
		return ""
	}
	return "https://" + host + "/webhooks/github/" + sourceID
}

// sourceDTO is the masked JSON projection — credentials never leave
// the server. AccountLogin/AccountType are populated for github_app
// rows after the install flow finishes (UI relies on these to show
// "personal" vs "organization" without exposing IDs).
type sourceDTO struct {
	ID             string  `json:"id"`
	Type           string  `json:"type"`
	Name           string  `json:"name"`
	InstallationID *string `json:"installation_id,omitempty"`
	PublicKey      *string `json:"public_key,omitempty"`
	HasToken       bool    `json:"has_token"`
	HasPrivateKey  bool    `json:"has_private_key"`
	// GitHub App identity (only for type=github_app)
	AppID        string `json:"app_id,omitempty"`
	AppSlug      string `json:"app_slug,omitempty"`
	AccountLogin string `json:"account_login,omitempty"`
	AccountType  string `json:"account_type,omitempty"`
	// PendingInstall is true for github_app rows whose App registration
	// finished but the operator hasn't pointed it at an account yet.
	// The UI surfaces a "Finish install" CTA when this is true.
	PendingInstall bool   `json:"pending_install,omitempty"`
	CreatedAt      string `json:"created_at"`
	// Webhook integration (only for type=github_app).
	//
	// WebhookURL is the public callback URL Prexel listens on for this
	// source — empty when no instance URL is configured. WebhookSecret
	// is write-only by default: WebhookSecretSet says "we have a value
	// stored, you just can't see it"; the actual value is included only
	// on the create + regenerate responses via revealDTO below.
	WebhookURL       string `json:"webhook_url,omitempty"`
	WebhookSecret    string `json:"webhook_secret,omitempty"`
	WebhookSecretSet bool   `json:"webhook_secret_set,omitempty"`
}

// toDTO returns the masked projection used by List/Get/Patch.
func (h *GitSource) toDTO(s *gitsrc.Source) sourceDTO {
	return h.toDTOReveal(s, false)
}

// toDTOReveal optionally includes the raw webhook_secret. Use only on
// responses where the operator is the one who triggered the rotation
// (create + regenerate).
func (h *GitSource) toDTOReveal(s *gitsrc.Source, reveal bool) sourceDTO {
	dto := sourceDTO{
		ID:             s.ID,
		Type:           s.Type,
		Name:           s.Name,
		InstallationID: s.InstallationID,
		PublicKey:      s.PublicKey,
		HasToken:       len(s.Token) > 0,
		HasPrivateKey:  len(s.PrivateKey) > 0,
		AppID:          s.AppID,
		AppSlug:        s.AppSlug,
		AccountLogin:   s.AccountLogin,
		AccountType:    s.AccountType,
		CreatedAt:      s.CreatedAt.Format(time.RFC3339),
	}
	if s.Type == "github_app" && (s.InstallationID == nil || *s.InstallationID == "") {
		dto.PendingInstall = true
	}
	if s.Type == "github_app" {
		dto.WebhookURL = h.webhookURLFor(s.ID)
		dto.WebhookSecretSet = strings.TrimSpace(s.WebhookSecret) != ""
		if reveal {
			dto.WebhookSecret = s.WebhookSecret
		}
	}
	return dto
}

// List handles GET /api/v1/git-sources.
func (h *GitSource) List(w http.ResponseWriter, _ *http.Request) {
	sources, err := h.d.Repo.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	out := make([]sourceDTO, 0, len(sources))
	for _, s := range sources {
		out = append(out, h.toDTO(s))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// Get handles GET /api/v1/git-sources/{id}.
func (h *GitSource) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, err := h.d.Repo.Get(id)
	if err != nil {
		if errors.Is(err, gitsrc.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, h.toDTO(s))
}

type createReq struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Generate   bool   `json:"generate,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	Token      string `json:"token,omitempty"`
}

// Create handles POST /api/v1/git-sources. ONLY ssh_key / token types
// are creatable via this endpoint — github_app sources are created
// exclusively by the manifest flow (ManifestCallback), because that's
// the only path where Prexel learns the App's credentials.
func (h *GitSource) Create(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing_name")
		return
	}

	src := &gitsrc.Source{
		ID:   uuid.NewString(),
		Type: req.Type,
		Name: name,
	}

	switch req.Type {
	case "github_app":
		// Refuse direct creation — the manifest flow creates the row
		// because it's the only path where we get the App's
		// private_key + slug. The previous manual path (paste an
		// installation_id) is gone in the per-source-credentials
		// model: there's no longer a singleton App to attach the
		// installation to.
		writeError(w, http.StatusBadRequest, "github_app_via_manifest_only")
		return

	case "ssh_key":
		var (
			privPEM []byte
			pubAuth string
		)
		if req.Generate {
			key, err := gitsrc.GenerateEd25519()
			if err != nil {
				writeError(w, http.StatusInternalServerError, "keygen_failed")
				return
			}
			privPEM = key.PrivatePEM
			pubAuth = key.PublicAuthorized
		} else {
			if strings.TrimSpace(req.PrivateKey) == "" {
				writeError(w, http.StatusBadRequest, "missing_private_key")
				return
			}
			privPEM = []byte(req.PrivateKey)
		}
		enc, err := h.d.Repo.EncryptString(string(privPEM))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt_failed")
			return
		}
		src.PrivateKey = enc
		if pubAuth != "" {
			trim := strings.TrimSpace(pubAuth)
			src.PublicKey = &trim
		}

	case "token":
		if strings.TrimSpace(req.Token) == "" {
			writeError(w, http.StatusBadRequest, "missing_token")
			return
		}
		enc, err := h.d.Repo.EncryptString(req.Token)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt_failed")
			return
		}
		src.Token = enc

	default:
		writeError(w, http.StatusBadRequest, "invalid_type")
		return
	}

	if err := h.d.Repo.Create(src); err != nil {
		writeError(w, http.StatusInternalServerError, "db_insert_failed")
		return
	}
	writeJSON(w, http.StatusCreated, h.toDTOReveal(src, true))
}

// Delete handles DELETE /api/v1/git-sources/{id}.
func (h *GitSource) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.d.Repo.Delete(id); err != nil {
		switch {
		case errors.Is(err, gitsrc.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found")
		case errors.Is(err, gitsrc.ErrInUse):
			writeError(w, http.StatusConflict, "in_use")
		default:
			writeError(w, http.StatusInternalServerError, "db_error")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type testReq struct {
	RepoURL string `json:"repo_url"`
}

// Test handles POST /api/v1/git-sources/{id}/test.
func (h *GitSource) Test(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	src, err := h.d.Repo.Get(id)
	if err != nil {
		if errors.Is(err, gitsrc.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	var req testReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if strings.TrimSpace(req.RepoURL) == "" {
		writeError(w, http.StatusBadRequest, "missing_repo_url")
		return
	}

	cloner := gitsrc.NewCloner(h.d.Repo, h.d.App)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := cloner.Test(ctx, src, req.RepoURL); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ── Manifest flow ──────────────────────────────────────────────────

type manifestURLReq struct {
	BaseURL string `json:"base_url"`
	Account string `json:"account"`
	Org     string `json:"org"`
}

type manifestStatePayload struct {
	BaseURL string `json:"base_url"`
	Org     string `json:"org,omitempty"`
	// SourceName is the human-friendly label the Prexel-side row
	// inherits when the manifest callback creates the git_sources
	// row. Auto-suffixed with the count of existing github_app
	// sources at request time so "personal" + "personal · 2" both
	// fit when an operator registers more than one App in the same
	// account.
	SourceName string `json:"source_name,omitempty"`
}

var githubOrgSlugRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

func encodeManifestStatePayload(p manifestStatePayload) string {
	payload, _ := json.Marshal(p)
	return string(payload)
}

func decodeManifestStatePayload(raw string) manifestStatePayload {
	var payload manifestStatePayload
	if err := json.Unmarshal([]byte(raw), &payload); err == nil && payload.BaseURL != "" {
		return payload
	}
	// Legacy state (pre-009): the raw string was a bare baseURL.
	return manifestStatePayload{BaseURL: raw}
}

// ManifestURL handles POST /api/v1/git-sources/github/manifest-url.
//
// Returns the URL the browser should navigate to start the manifest
// flow. We pre-compute the "Prexel · <instance> · N" suffix used both
// for the App's GitHub-side name AND for the source's Prexel-side
// name; mismatch between the two would be confusing.
func (h *GitSource) ManifestURL(w http.ResponseWriter, r *http.Request) {
	var req manifestURLReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if baseURL == "" || !(strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://")) {
		writeError(w, http.StatusBadRequest, "invalid_base_url")
		return
	}
	account := strings.TrimSpace(req.Account)
	if account == "" {
		account = "personal"
	}
	org := strings.TrimSpace(req.Org)
	switch account {
	case "personal":
		org = ""
	case "org":
		if org == "" {
			writeError(w, http.StatusBadRequest, "missing_org")
			return
		}
		if !githubOrgSlugRE.MatchString(org) {
			writeError(w, http.StatusBadRequest, "invalid_org")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "invalid_account")
		return
	}

	// Source name = scope hint + (counter only when not the first).
	count, err := h.d.Repo.CountGitHubSources()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	scope := "personal"
	if org != "" {
		scope = org
	}
	sourceName := "GitHub · " + scope
	if count > 0 {
		sourceName = fmt.Sprintf("GitHub · %s · %d", scope, count+1)
	}

	state, err := gitsrc.NewManifestState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "state_failed")
		return
	}
	payload := encodeManifestStatePayload(manifestStatePayload{
		BaseURL:    baseURL,
		Org:        org,
		SourceName: sourceName,
	})
	if err := h.d.Repo.SaveManifestState(state, payload); err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"url": baseURL + "/api/v1/git-sources/github/manifest-start?state=" + state,
	})
}

// ManifestStart handles GET /api/v1/git-sources/github/manifest-start.
func (h *GitSource) ManifestStart(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		writeError(w, http.StatusBadRequest, "missing_state")
		return
	}
	rawPayload, err := h.d.Repo.ConsumeManifestState(state)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	payload := decodeManifestStatePayload(rawPayload)
	if payload.BaseURL == "" {
		writeError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	// Re-persist the state so the callback can re-read it. The state
	// is consumed once at /manifest-start (to validate it exists)
	// and re-consumed at /manifest-callback (to extract the payload).
	if err := h.d.Repo.SaveManifestState(state, rawPayload); err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	// The App's name on GitHub must be globally unique. We mirror the
	// scope hint used for the Prexel-side row + instance suffix to
	// disambiguate from other Prexel installs the operator may own.
	instanceID, _ := h.d.Repo.Setting("instance_id")
	suffix := strings.TrimPrefix(strings.TrimSpace(instanceID), "prx_")
	appName := payload.SourceName
	if suffix != "" {
		appName = appName + " · " + suffix
	}
	writeGitHubFlowHTML(w, gitsrc.ManifestFormHTML(payload.BaseURL, state, appName, payload.Org))
}

// createPendingGitHubSource is the shared "I have App credentials,
// persist them as a pending git_source" path. Used both by the
// in-panel manifest flow (ManifestCallback, which calls
// ConvertManifestCode itself) and by the CLI import endpoint (which
// converts the code locally and ships us the result).
//
// Returns the newly-created source and the GitHub URL the caller
// should redirect the user to so they can pick an account/repos.
func (h *GitSource) createPendingGitHubSource(cfg gitsrc.GitHubAppConfig, sourceName string) (*gitsrc.Source, string, error) {
	encKey, err := h.d.Repo.EncryptString(cfg.PrivateKey)
	if err != nil {
		return nil, "", fmt.Errorf("encrypt: %w", err)
	}
	if strings.TrimSpace(sourceName) == "" {
		sourceName = "GitHub · " + cfg.Slug
	}
	// Generate a webhook secret at creation time so the operator can
	// surface it once in the response and paste it into the GitHub App
	// settings. Rotated via POST /git-sources/{id}/regenerate-webhook.
	secret, err := gitsrc.NewWebhookSecret()
	if err != nil {
		return nil, "", fmt.Errorf("webhook secret: %w", err)
	}
	src := &gitsrc.Source{
		ID:            uuid.NewString(),
		Type:          "github_app",
		Name:          sourceName,
		AppID:         cfg.AppID,
		AppSlug:       cfg.Slug,
		AppPrivateKey: encKey,
		WebhookSecret: secret,
	}
	if err := h.d.Repo.Create(src); err != nil {
		return nil, "", fmt.Errorf("create: %w", err)
	}
	return src, h.d.App.InstallURLFor(src), nil
}

// Import handles POST /api/v1/git-sources/github/import.
//
// Server-side counterpart of the CLI's local OAuth flow. The CLI runs
// the whole manifest dance against GitHub on its own (via an ephemeral
// localhost HTTP server), converts the code to App credentials, then
// posts those credentials here so the Prexel API takes over from this
// point as if the manifest had completed inside the panel.
//
// Body: { app_id, slug, private_key, name? }
// Returns: { source_id, install_url } so the CLI can redirect the
// browser to the install picker on GitHub.
//
// Auth: standard JWT/PAT (same as Create) — the operator is choosing
// to register an integration on their own account, so we don't need
// special trust beyond "they're logged in".
func (h *GitSource) Import(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID      string `json:"app_id"`
		Slug       string `json:"slug"`
		PrivateKey string `json:"private_key"`
		Name       string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if strings.TrimSpace(req.AppID) == "" ||
		strings.TrimSpace(req.Slug) == "" ||
		strings.TrimSpace(req.PrivateKey) == "" {
		writeError(w, http.StatusBadRequest, "missing_app_credentials")
		return
	}
	src, installURL, err := h.createPendingGitHubSource(gitsrc.GitHubAppConfig{
		AppID:      strings.TrimSpace(req.AppID),
		Slug:       strings.TrimSpace(req.Slug),
		PrivateKey: req.PrivateKey, // PEM — preserve verbatim
	}, req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"source_id":   src.ID,
		"install_url": installURL,
	})
}

// ManifestCallback handles GET /api/v1/git-sources/github/manifest-callback.
//
// Trades the temporary `code` for the App's id/slug/private_key, then
// creates a new git_sources row (pending install). The browser is
// then redirected to GitHub's install URL for the freshly-created
// App; once the operator picks an account, GitHubCallback (below)
// posts the installation_id back to the SPA which calls Finalize to
// stamp installation_id + account info on this same row.
func (h *GitSource) ManifestCallback(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if state == "" || code == "" {
		writeError(w, http.StatusBadRequest, "missing_manifest_callback_params")
		return
	}
	rawPayload, err := h.d.Repo.ConsumeManifestState(state)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	payload := decodeManifestStatePayload(rawPayload)
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	cfg, err := gitsrc.ConvertManifestCode(ctx, code)
	if err != nil {
		writeError(w, http.StatusBadGateway, "github_manifest_conversion_failed")
		return
	}

	src, installURL, err := h.createPendingGitHubSource(*cfg, payload.SourceName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_failed")
		return
	}
	writeGitHubFlowHTML(w, manifestCallbackHTML(src.ID, src.AppSlug, installURL))
}

// ── Install flow ──────────────────────────────────────────────────

// GitHubCallback handles GET /api/v1/git-sources/github/callback.
// PUBLIC route — GitHub redirects the user's browser here after they
// finish installing the App on their account.
//
// Returns a tiny page that postMessages installation_id back to the
// SPA. The SPA then calls /git-sources/{id}/finalize with that id.
func (h *GitSource) GitHubCallback(w http.ResponseWriter, r *http.Request) {
	installationID := r.URL.Query().Get("installation_id")
	setupAction := r.URL.Query().Get("setup_action")
	writeGitHubFlowHTML(w, callbackHTML(installationID, setupAction))
}

type finalizeReq struct {
	InstallationID string `json:"installation_id"`
}

// Finalize handles POST /api/v1/git-sources/{id}/finalize.
//
// Called by the SPA after the install callback returns. Looks up the
// pending source, validates the installation belongs to it (by asking
// GitHub for account info using the App's JWT), and stamps the row
// with installation_id + account_login + account_type. After this
// call the source is fully usable (List/Branches/Clone all work).
func (h *GitSource) Finalize(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	src, err := h.d.Repo.Get(id)
	if err != nil {
		if errors.Is(err, gitsrc.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if src.Type != "github_app" {
		writeError(w, http.StatusBadRequest, "not_github_app_source")
		return
	}
	var req finalizeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	installationID := strings.TrimSpace(req.InstallationID)
	if installationID == "" {
		writeError(w, http.StatusBadRequest, "missing_installation_id")
		return
	}

	auth, err := h.d.Repo.AppAuthFor(src)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decrypt_failed")
		return
	}
	auth.InstallationID = installationID

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	account, err := h.d.App.GetInstallationAccount(ctx, auth)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":   "github_account_lookup_failed",
			"message": err.Error(),
		})
		return
	}

	if err := h.d.Repo.UpdateInstallation(src.ID, installationID, account.Login, account.Type); err != nil {
		if errors.Is(err, gitsrc.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	// Re-fetch so the response reflects the now-finalised state.
	updated, err := h.d.Repo.Get(src.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, h.toDTO(updated))
}

// ── Repository helpers ────────────────────────────────────────────

// Repositories handles GET /api/v1/git-sources/{id}/repositories.
func (h *GitSource) Repositories(w http.ResponseWriter, r *http.Request) {
	auth, src, ok := h.githubAppAuth(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	_ = src
	page := intQuery(r, "page", 1)
	perPage := intQuery(r, "per_page", 30)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	repos, err := h.d.App.ListInstallationRepositories(ctx, auth, r.URL.Query().Get("q"), page, perPage)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"items": []gitsrc.GitHubRepository{},
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": repos})
}

// Branches handles GET /api/v1/git-sources/{id}/repositories/{owner}/{repo}/branches.
func (h *GitSource) Branches(w http.ResponseWriter, r *http.Request) {
	auth, _, ok := h.githubAppAuth(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	branches, err := h.d.App.ListBranches(ctx, auth, chi.URLParam(r, "owner"), chi.URLParam(r, "repo"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"items": []gitsrc.GitHubBranch{},
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": branches})
}

type inspectRepoReq struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

// InspectRepository handles POST /api/v1/git-sources/{id}/repositories/inspect.
func (h *GitSource) InspectRepository(w http.ResponseWriter, r *http.Request) {
	auth, _, ok := h.githubAppAuth(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req inspectRepoReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if strings.TrimSpace(req.Owner) == "" || strings.TrimSpace(req.Repo) == "" {
		writeError(w, http.StatusBadRequest, "missing_repository")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	inspect, err := h.d.App.InspectRepository(ctx, auth, strings.TrimSpace(req.Owner), strings.TrimSpace(req.Repo), strings.TrimSpace(req.Branch))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, inspect)
}

// InstallURL handles GET /api/v1/git-sources/{id}/install-url.
//
// Per-source now (was global pre-009). Used by the UI to reopen the
// install flow for a source whose installation was revoked on the
// GitHub side, or to add the App to a new repository.
func (h *GitSource) InstallURL(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	src, err := h.d.Repo.Get(id)
	if err != nil {
		if errors.Is(err, gitsrc.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if src.Type != "github_app" {
		writeError(w, http.StatusBadRequest, "not_github_app_source")
		return
	}
	u := h.d.App.InstallURLFor(src)
	if u == "" {
		writeError(w, http.StatusBadRequest, "github_app_slug_missing")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

// RegenerateWebhook handles POST /api/v1/git-sources/{id}/regenerate-webhook.
//
// Rotates the source's webhook_secret and returns the new value once.
// After this call the operator MUST paste the new value into the
// GitHub App's webhook settings or every subsequent push delivery
// will 401 here. Only valid for type=github_app sources.
func (h *GitSource) RegenerateWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	src, err := h.d.Repo.Get(id)
	if err != nil {
		if errors.Is(err, gitsrc.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if src.Type != "github_app" {
		writeError(w, http.StatusBadRequest, "not_github_app_source")
		return
	}
	secret, err := gitsrc.NewWebhookSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "secret_generation_failed")
		return
	}
	if err := h.d.Repo.SetWebhookSecret(src.ID, secret); err != nil {
		if errors.Is(err, gitsrc.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	src.WebhookSecret = secret
	writeJSON(w, http.StatusOK, h.toDTOReveal(src, true))
}

// ── HTML responses ────────────────────────────────────────────────

func callbackHTML(installationID, setupAction string) string {
	idJSON, _ := json.Marshal(installationID)
	actionJSON, _ := json.Marshal(setupAction)
	return `<!doctype html>
<html><head><meta charset="utf-8"><title>Prexel · GitHub App installed</title></head>
<body style="font-family: system-ui, sans-serif; padding: 24px;">
  <h1>Prexel</h1>
  <p>GitHub App installed. You can close this window.</p>
  <script>
    (function () {
      var payload = { type: "prexel:github-app-installed", installation_id: ` + string(idJSON) + `, setup_action: ` + string(actionJSON) + ` };
      try { if (window.opener) { window.opener.postMessage(payload, "*"); } } catch (e) {}
      setTimeout(function () { window.close(); }, 500);
    })();
  </script>
</body></html>`
}

// manifestCallbackHTML announces the newly-created source's ID + slug
// to the opener so the SPA can (a) remember which row to finalise
// when the install callback fires, and (b) navigate the user to the
// install page in the same window.
func manifestCallbackHTML(sourceID, slug, installURL string) string {
	idJSON, _ := json.Marshal(sourceID)
	slugJSON, _ := json.Marshal(slug)
	installJSON, _ := json.Marshal(installURL)
	return `<!doctype html>
<html><head><meta charset="utf-8"><title>Prexel · GitHub App created</title></head>
<body style="font-family: system-ui, sans-serif; padding: 24px;">
  <h1>Prexel</h1>
  <p>GitHub App criada. Abrindo instalação nos repositórios…</p>
  <script>
    (function () {
      var payload = { type: "prexel:github-app-created", source_id: ` + string(idJSON) + `, slug: ` + string(slugJSON) + ` };
      try { if (window.opener) { window.opener.postMessage(payload, "*"); } } catch (e) {}
      setTimeout(function () { window.location.href = ` + string(installJSON) + `; }, 500);
    })();
  </script>
</body></html>`
}

func writeGitHubFlowHTML(w http.ResponseWriter, body string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; form-action https://github.com")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// ── helpers ────────────────────────────────────────────────────────

// githubAppAuth resolves a Source by id, validates it's a finalised
// github_app row, and builds the AppAuth needed by every API call.
// Returns the source too so callers that need additional fields
// (account_login for UI errors, etc) don't have to re-fetch.
func (h *GitSource) githubAppAuth(w http.ResponseWriter, id string) (gitsrc.AppAuth, *gitsrc.Source, bool) {
	src, err := h.d.Repo.Get(id)
	if err != nil {
		if errors.Is(err, gitsrc.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return gitsrc.AppAuth{}, nil, false
		}
		writeError(w, http.StatusInternalServerError, "db_error")
		return gitsrc.AppAuth{}, nil, false
	}
	if src.Type != "github_app" {
		writeError(w, http.StatusBadRequest, "not_github_app_source")
		return gitsrc.AppAuth{}, nil, false
	}
	if src.InstallationID == nil || strings.TrimSpace(*src.InstallationID) == "" {
		writeError(w, http.StatusBadRequest, "install_pending")
		return gitsrc.AppAuth{}, nil, false
	}
	auth, err := h.d.Repo.AppAuthFor(src)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decrypt_failed")
		return gitsrc.AppAuth{}, nil, false
	}
	if auth.AppID == "" || auth.PrivateKeyPEM == "" {
		writeError(w, http.StatusBadRequest, "github_app_not_configured")
		return gitsrc.AppAuth{}, nil, false
	}
	return auth, src, true
}

func intQuery(r *http.Request, key string, fallback int) int {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
