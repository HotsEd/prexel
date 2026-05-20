package gitsrc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NewManifestState returns an unguessable state token for the manifest flow.
func NewManifestState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ManifestFormHTML returns a small auto-submit page that posts the GitHub App
// manifest to GitHub. GitHub requires POST for manifest registration.
func ManifestFormHTML(baseURL, state, appName, org string) string {
	if strings.TrimSpace(appName) == "" {
		appName = "Prexel Integration App"
	}
	manifest, _ := json.Marshal(map[string]any{
		"name":        appName,
		"url":         baseURL,
		"description": "Self-hosted deploys managed by Prexel.",
		"redirect_url": baseURL +
			"/api/v1/git-sources/github/manifest-callback",
		"callback_urls": []string{
			baseURL + "/api/v1/git-sources/github/callback",
		},
		"setup_url":       baseURL + "/api/v1/git-sources/github/callback",
		"setup_on_update": true,
		"public":          false,
		"default_permissions": map[string]string{
			"contents": "read",
			"metadata": "read",
		},
		"default_events": []string{},
	})
	action := "https://github.com/settings/apps/new?state=" + url.QueryEscape(state)
	if strings.TrimSpace(org) != "" {
		action = "https://github.com/organizations/" + url.PathEscape(strings.TrimSpace(org)) + "/settings/apps/new?state=" + url.QueryEscape(state)
	}
	return `<!doctype html>
<html><head><meta charset="utf-8"><title>Prexel · GitHub App</title></head>
<body onload="document.getElementById('f').submit()" style="font-family: system-ui, sans-serif; padding: 24px;">
  <h1>Prexel</h1>
  <p>Redirecionando para criar a GitHub App…</p>
  <form id="f" method="post" action="` + html.EscapeString(action) + `">
    <input type="hidden" name="manifest" value="` + html.EscapeString(string(manifest)) + `">
    <button type="submit" style="appearance:none;border:0;border-radius:8px;background:#10b981;color:white;padding:10px 14px;font:inherit;cursor:pointer;">Continuar para o GitHub</button>
  </form>
</body></html>`
}

// ConvertManifestCode exchanges GitHub's temporary manifest code for a real
// app id, slug and private key.
func ConvertManifestCode(ctx context.Context, code string) (*GitHubAppConfig, error) {
	if strings.TrimSpace(code) == "" {
		return nil, fmt.Errorf("github manifest: missing code")
	}
	u := "https://api.github.com/app-manifests/" + url.PathEscape(code) + "/conversions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(nil))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github manifest request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github manifest conversion: status %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
		PEM  string `json:"pem"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode github manifest conversion: %w", err)
	}
	if payload.ID == 0 || strings.TrimSpace(payload.PEM) == "" {
		return nil, fmt.Errorf("github manifest conversion: incomplete response")
	}
	return &GitHubAppConfig{
		AppID:      fmt.Sprintf("%d", payload.ID),
		Slug:       payload.Slug,
		PrivateKey: payload.PEM,
	}, nil
}
