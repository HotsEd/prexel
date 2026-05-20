package gitsrc

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CloneOptions controls a single clone invocation.
type CloneOptions struct {
	Source    *Source
	RepoURL   string
	Branch    string // default: main
	CommitSHA string // optional: post-clone checkout to a specific SHA
	Dest      string // destination directory (must NOT yet exist for git clone)
}

// Cloner glues a Repo (for credential decryption) with a GitHubApp (for
// installation tokens) and exposes Clone/Test against arbitrary repos. A nil
// GitHubApp is OK as long as no caller asks for a github_app source.
type Cloner struct {
	Repo *Repo
	App  *GitHubApp
}

// NewCloner wires the two collaborators together.
func NewCloner(repo *Repo, app *GitHubApp) *Cloner {
	return &Cloner{Repo: repo, App: app}
}

// Clone clones opts.RepoURL into opts.Dest using credentials from opts.Source.
func (c *Cloner) Clone(ctx context.Context, opts CloneOptions) error {
	if opts.Source == nil {
		return errors.New("git clone: nil source")
	}
	if opts.RepoURL == "" {
		return errors.New("git clone: empty repo url")
	}
	if opts.Dest == "" {
		return errors.New("git clone: empty dest")
	}
	// Scheme allowlist + SSRF protection (see internal/gitsrc/validate.go).
	// Called BEFORE any credential decryption so a bad URL can't even
	// trigger the unwrap of an AppPrivateKey.
	if err := ValidateRepoURL(opts.RepoURL); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	branch := opts.Branch
	if branch == "" {
		branch = "main"
	}

	args := []string{"clone", "--branch", branch, "--single-branch"}

	var (
		env       []string
		urlForGit = opts.RepoURL
		cleanup   func()
	)
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()

	switch opts.Source.Type {
	case "github_app":
		if opts.Source.InstallationID == nil {
			return errors.New("git clone: github_app source missing installation_id")
		}
		auth, err := c.Repo.AppAuthFor(opts.Source)
		if err != nil {
			return err
		}
		token, _, err := c.App.InstallationToken(ctx, auth)
		if err != nil {
			return err
		}
		urlForGit, err = injectHTTPSToken(opts.RepoURL, "x-access-token", token)
		if err != nil {
			return err
		}
	case "token":
		pat, err := c.Repo.DecryptToken(opts.Source)
		if err != nil {
			return fmt.Errorf("decrypt token: %w", err)
		}
		urlForGit, err = injectHTTPSToken(opts.RepoURL, "x-access-token", pat)
		if err != nil {
			return err
		}
	case "ssh_key":
		priv, err := c.Repo.DecryptPrivateKey(opts.Source)
		if err != nil {
			return fmt.Errorf("decrypt key: %w", err)
		}
		keyFile, cu, err := writeTempKey(priv)
		if err != nil {
			return err
		}
		cleanup = cu
		env = append(env, "GIT_SSH_COMMAND="+sshCommand(keyFile))
	default:
		return fmt.Errorf("git clone: unknown source type %q", opts.Source.Type)
	}

	args = append(args, urlForGit, opts.Dest)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w: %s", err, scrubToken(string(out)))
	}

	if opts.CommitSHA != "" {
		checkout := exec.CommandContext(ctx, "git", "-C", opts.Dest, "checkout", opts.CommitSHA)
		checkout.Env = append(os.Environ(), env...)
		if out, err := checkout.CombinedOutput(); err != nil {
			return fmt.Errorf("git checkout failed: %w: %s", err, string(out))
		}
	}
	return nil
}

// Test runs `git ls-remote` to validate credentials without cloning.
func (c *Cloner) Test(ctx context.Context, source *Source, repoURL string) error {
	if source == nil {
		return errors.New("git test: nil source")
	}
	if repoURL == "" {
		return errors.New("git test: empty repo url")
	}
	// Same SSRF gate as Clone — Test is the cheap path operators run
	// from the UI, but `git ls-remote` still hits the resolved IP and
	// can probe internal services on its own.
	if err := ValidateRepoURL(repoURL); err != nil {
		return fmt.Errorf("git test: %w", err)
	}

	var (
		env       []string
		urlForGit = repoURL
		cleanup   func()
	)
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()

	switch source.Type {
	case "github_app":
		if source.InstallationID == nil {
			return errors.New("git test: github_app source missing installation_id")
		}
		auth, err := c.Repo.AppAuthFor(source)
		if err != nil {
			return err
		}
		token, _, err := c.App.InstallationToken(ctx, auth)
		if err != nil {
			return err
		}
		urlForGit, err = injectHTTPSToken(repoURL, "x-access-token", token)
		if err != nil {
			return err
		}
	case "token":
		pat, err := c.Repo.DecryptToken(source)
		if err != nil {
			return err
		}
		urlForGit, err = injectHTTPSToken(repoURL, "x-access-token", pat)
		if err != nil {
			return err
		}
	case "ssh_key":
		priv, err := c.Repo.DecryptPrivateKey(source)
		if err != nil {
			return err
		}
		keyFile, cu, err := writeTempKey(priv)
		if err != nil {
			return err
		}
		cleanup = cu
		env = append(env, "GIT_SSH_COMMAND="+sshCommand(keyFile))
	default:
		return fmt.Errorf("git test: unknown source type %q", source.Type)
	}

	cmd := exec.CommandContext(ctx, "git", "ls-remote", urlForGit, "HEAD")
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ls-remote failed: %w: %s", err, scrubToken(string(out)))
	}
	return nil
}

// injectHTTPSToken rewrites an https URL to include `user:token@`. It refuses
// to touch non-HTTPS URLs to avoid silently downgrading or leaking tokens.
func injectHTTPSToken(repoURL, user, token string) (string, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("token auth requires https url, got %q", u.Scheme)
	}
	u.User = url.UserPassword(user, token)
	return u.String(), nil
}

// writeTempKey persists `priv` to a 0600 file in os.TempDir and returns the
// path plus a cleanup function the caller must defer.
func writeTempKey(priv string) (string, func(), error) {
	f, err := os.CreateTemp("", "prexel-gitkey-*.pem")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	if err := os.Chmod(path, 0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", nil, err
	}
	if _, err := f.WriteString(priv); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", nil, err
	}
	cleanup := func() { _ = os.Remove(path) }
	return path, cleanup, nil
}

func sshCommand(keyFile string) string {
	// accept-new = trust on first use. Stricter v0.2 work covered by Tech Review §3.
	return "ssh -i " + keyFile + " -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=" + filepath.Join(os.TempDir(), ".prexel-known-hosts")
}

// scrubToken hides any accidental token leaking in git's error output.
// We replace any `user:secret@` sequence in URLs with `***@`.
func scrubToken(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.Index(s, "://")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.IndexAny(s[i+3:], "@ \t\n")
		if j < 0 || s[i+3+j] != '@' {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i+3])
		b.WriteString("***")
		s = s[i+3+j:]
	}
}
