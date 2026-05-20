package gitsrc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// GitHubRepository is the small repository projection the UI needs for import.
type GitHubRepository struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	FullName      string     `json:"full_name"`
	Private       bool       `json:"private"`
	HTMLURL       string     `json:"html_url"`
	CloneURL      string     `json:"clone_url"`
	DefaultBranch string     `json:"default_branch"`
	Language      *string    `json:"language,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

// GitHubBranch is a repository branch projection.
type GitHubBranch struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

// RepositoryInspect reports root-level deployment files found in a repository.
type RepositoryInspect struct {
	RepoURL        string   `json:"repo_url"`
	Branch         string   `json:"branch"`
	DockerfilePath *string  `json:"dockerfile_path,omitempty"`
	ComposeFile    *string  `json:"compose_file,omitempty"`
	BuildContext   string   `json:"build_context"`
	SuggestedType  string   `json:"suggested_type"`
	FoundFiles     []string `json:"found_files"`
}

// ListInstallationRepositories lists repositories this installation can access.
func (a *GitHubApp) ListInstallationRepositories(ctx context.Context, auth AppAuth, query string, page, perPage int) ([]GitHubRepository, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 30
	}
	var payload struct {
		Repositories []GitHubRepository `json:"repositories"`
	}
	u := "https://api.github.com/installation/repositories?per_page=" + strconv.Itoa(perPage) + "&page=" + strconv.Itoa(page)
	if err := a.installationJSON(ctx, auth, u, &payload); err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return payload.Repositories, nil
	}
	out := make([]GitHubRepository, 0, len(payload.Repositories))
	for _, repo := range payload.Repositories {
		if strings.Contains(strings.ToLower(repo.FullName), q) {
			out = append(out, repo)
		}
	}
	return out, nil
}

// ListBranches lists repository branches visible to the installation token.
func (a *GitHubApp) ListBranches(ctx context.Context, auth AppAuth, owner, repo string) ([]GitHubBranch, error) {
	var payload []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	u := "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/branches?per_page=100"
	if err := a.installationJSON(ctx, auth, u, &payload); err != nil {
		return nil, err
	}
	out := make([]GitHubBranch, 0, len(payload))
	for _, b := range payload {
		out = append(out, GitHubBranch{Name: b.Name, SHA: b.Commit.SHA})
	}
	return out, nil
}

// InspectRepository detects the root-level files Prexel can deploy from.
func (a *GitHubApp) InspectRepository(ctx context.Context, auth AppAuth, owner, repo, branch string) (*RepositoryInspect, error) {
	if strings.TrimSpace(branch) == "" {
		branch = "main"
	}
	candidates := []string{"Dockerfile", "docker-compose.yml", "compose.yml"}
	found := make([]string, 0, len(candidates))
	for _, path := range candidates {
		ok, err := a.repositoryFileExists(ctx, auth, owner, repo, path, branch)
		if err != nil {
			return nil, err
		}
		if ok {
			found = append(found, path)
		}
	}

	inspect := &RepositoryInspect{
		RepoURL:       "https://github.com/" + owner + "/" + repo,
		Branch:        branch,
		BuildContext:  ".",
		SuggestedType: "dockerfile",
		FoundFiles:    found,
	}
	for _, f := range found {
		if f == "docker-compose.yml" || f == "compose.yml" {
			v := f
			inspect.ComposeFile = &v
			inspect.SuggestedType = "docker_compose"
			return inspect, nil
		}
	}
	for _, f := range found {
		if f == "Dockerfile" {
			v := f
			inspect.DockerfilePath = &v
			return inspect, nil
		}
	}
	return inspect, nil
}

func (a *GitHubApp) repositoryFileExists(ctx context.Context, auth AppAuth, owner, repo, path, branch string) (bool, error) {
	u := "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/contents/" + url.PathEscape(path) + "?ref=" + url.QueryEscape(branch)
	token, _, err := a.InstallationToken(ctx, auth)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := a.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("github request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return false, fmt.Errorf("github contents: status %d: %s", resp.StatusCode, string(body))
}

// GetRepositoryFile fetches the raw bytes of a single file at the
// given path/ref. Used by the AppDetail "Containers" tab to render
// a Compose YAML preview before the first deploy lands — operator
// needs to see services + assign domains without having to deploy
// first, and we don't want to clone the whole repo just for that.
//
// GitHub's contents endpoint returns the file base64-encoded when
// it fits (≤1MB) — we decode it transparently. Larger files fall
// through to the download_url path (rarely needed for a YAML
// manifest, but kept for completeness).
//
// Returns os.ErrNotExist when the path doesn't exist on the ref;
// callers can use errors.Is to render an "add a docker-compose.yml
// to your repo" hint instead of a generic error.
func (a *GitHubApp) GetRepositoryFile(ctx context.Context, auth AppAuth, owner, repo, path, branch string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("github: missing file path")
	}
	if strings.TrimSpace(branch) == "" {
		branch = "main"
	}
	u := "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) +
		"/contents/" + url.PathEscape(path) + "?ref=" + url.QueryEscape(branch)
	token, _, err := a.InstallationToken(ctx, auth)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, os.ErrNotExist
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github contents: status %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		Encoding    string `json:"encoding"`
		Content     string `json:"content"`
		DownloadURL string `json:"download_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode contents: %w", err)
	}
	if payload.Encoding == "base64" && payload.Content != "" {
		// GitHub wraps base64 with newlines every 60 chars — Go's
		// std decoder rejects those, so strip first.
		raw := strings.ReplaceAll(payload.Content, "\n", "")
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("decode base64: %w", err)
		}
		return decoded, nil
	}
	// Large file fallback — re-fetch the raw bytes via the
	// pre-signed download URL. No auth needed (the URL is signed).
	if payload.DownloadURL == "" {
		return nil, fmt.Errorf("github contents: empty payload")
	}
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, payload.DownloadURL, nil)
	if err != nil {
		return nil, err
	}
	dlResp, err := a.client.Do(dlReq)
	if err != nil {
		return nil, fmt.Errorf("github raw download: %w", err)
	}
	defer func() { _ = dlResp.Body.Close() }()
	if dlResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github raw download: status %d", dlResp.StatusCode)
	}
	// Cap the read at 5MB — anyone shipping a compose file larger
	// than that is doing something unusual and we're not in that
	// business yet.
	return io.ReadAll(io.LimitReader(dlResp.Body, 5<<20))
}

func (a *GitHubApp) installationJSON(ctx context.Context, auth AppAuth, requestURL string, dest any) error {
	token, _, err := a.InstallationToken(ctx, auth)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("github request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github api: status %d: %s", resp.StatusCode, string(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode github response: %w", err)
	}
	return nil
}
