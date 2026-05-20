package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/deploy"
	"github.com/prexel/prexel/internal/gitsrc"
)

// Deployer is the slice of *deploy.Engine the webhook handler needs.
// Pulled out so the test suite can swap in an in-memory fake without
// having to construct the full deploy engine (build engine, docker,
// caddy, eventbus, etc.).
//
// The contract intentionally mirrors the per-app entry point used by
// the REST handler (POST /apps/{id}/deploy) — webhook deploys MUST go
// through the same validations and back-pressure (per-app mutex + the
// global semaphore) as human-triggered ones. Branch and commit_sha
// come straight from the push event.
type Deployer interface {
	Deploy(ctx context.Context, appID string, opts deploy.DeployOptions) (*deploy.Deployment, error)
}

// WebhookHandler handles inbound GitHub App push deliveries. Mounted
// outside the JWT-protected API surface — GitHub authenticates via
// X-Hub-Signature-256 (HMAC-SHA256 of the body with the per-source
// webhook_secret), so anything heavier (JWT, team checks) would be
// pointless and would also break the contract with GitHub.
type WebhookHandler struct {
	gitSrcs  *gitsrc.Repo
	apps     *app.Service
	deployer Deployer
}

// NewWebhookHandler wires the handler.
func NewWebhookHandler(gitSrcs *gitsrc.Repo, apps *app.Service, deployer Deployer) *WebhookHandler {
	return &WebhookHandler{gitSrcs: gitSrcs, apps: apps, deployer: deployer}
}

// maxWebhookBody caps the body we'll read from a GitHub delivery.
// GitHub's documented payload ceiling is 25MB, but for "push" events
// in practice it's well under 1MB; 5MB is generous head-room without
// inviting DoS via unbounded reads.
const maxWebhookBody = 5 << 20

// pushEvent captures the subset of GitHub's push payload Prexel needs
// to decide what to deploy. We deliberately don't unmarshal the full
// schema — anything beyond these fields would be dead weight.
type pushEvent struct {
	Ref        string `json:"ref"`   // e.g. "refs/heads/main"
	After      string `json:"after"` // commit SHA
	Repository struct {
		FullName string `json:"full_name"` // "owner/repo"
		CloneURL string `json:"clone_url"`
		SSHURL   string `json:"ssh_url"`
	} `json:"repository"`
	HeadCommit struct {
		Message string `json:"message"`
	} `json:"head_commit"`
	Deleted bool `json:"deleted"`
}

// Handle implements POST /webhooks/github/{source_id}.
func (h *WebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	sourceID := chi.URLParam(r, "source_id")
	src, err := h.gitSrcs.Get(sourceID)
	if err != nil {
		if errors.Is(err, gitsrc.ErrNotFound) {
			writeError(w, http.StatusNotFound, "source_not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}

	// Read body BEFORE checking the signature — verifying HMAC needs
	// the bytes. Cap to maxWebhookBody so a hostile sender can't pin
	// memory by streaming an infinitely large body.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large")
		return
	}

	// Signature check. GitHub sends `sha256=<hex>`; constant-time
	// comparison via hmac.Equal so timing leaks can't be used to
	// recover the secret. An empty stored secret rejects every
	// delivery — operators MUST regenerate before wiring webhooks.
	if !verifySignature(src.WebhookSecret, r.Header.Get("X-Hub-Signature-256"), body) {
		writeError(w, http.StatusUnauthorized, "invalid_signature")
		return
	}

	event := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	if event != "push" {
		// ack + no-op for ping, pull_request, installation, etc.
		// GitHub retries on non-2xx; we explicitly succeed so it
		// doesn't keep redelivering events we don't care about.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var ev pushEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if ev.Deleted {
		// Branch deletion is delivered as a push with `deleted=true`
		// and an all-zero `after`. There's nothing to deploy.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	branch := branchFromRef(ev.Ref)
	if branch == "" {
		// Tag pushes (refs/tags/…) or annotated refs we don't track
		// land here. Ignore gracefully.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Find matching apps. We iterate over every app (callers have
	// O(low-hundreds) apps; the heavy filtering — repo URL parse +
	// normalisation — wouldn't translate cleanly to SQL anyway).
	all, err := h.apps.ListAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}

	type matchOut struct {
		AppID        string `json:"app_id"`
		DeploymentID string `json:"deployment_id,omitempty"`
	}
	var matched []matchOut

	for i := range all {
		a := &all[i]
		if a.GitSourceID == nil || *a.GitSourceID != src.ID {
			continue
		}
		if a.AutoDeployBranch == nil || strings.TrimSpace(*a.AutoDeployBranch) != branch {
			continue
		}
		if a.RepoURL == nil || !repoURLMatches(*a.RepoURL, ev.Repository.CloneURL, ev.Repository.SSHURL) {
			continue
		}

		dep := h.triggerDeploy(a.ID, deploy.DeployOptions{
			Branch:    branch,
			CommitSHA: strings.TrimSpace(ev.After),
		})
		out := matchOut{AppID: a.ID}
		if dep != nil {
			out.DeploymentID = dep.ID
		}
		matched = append(matched, out)
	}

	if matched == nil {
		matched = []matchOut{}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"matched": matched,
		"branch":  branch,
		"event":   event,
	})
}

// triggerDeploy kicks off a deploy through the same code path the REST
// handler uses (POST /apps/{id}/deploy). The actual build runs on a
// detached background goroutine because builds take minutes and GitHub
// drops webhook deliveries that don't respond within ~10s.
//
// We synchronously wait up to a tiny budget for the goroutine to
// produce the deployment row id (the engine inserts it at the very
// start of runDeploy, then proceeds to build) so the webhook response
// can include the id. If the engine takes longer than that to even
// acknowledge the deploy — typically because the global semaphore is
// saturated and is sleeping — we just return a nil id; the caller's
// JSON includes the app_id alone and the operator can find the
// deployment via the dashboard.
func (h *WebhookHandler) triggerDeploy(appID string, opts deploy.DeployOptions) *deploy.Deployment {
	type result struct {
		dep *deploy.Deployment
		err error
	}
	out := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		dep, err := h.deployer.Deploy(ctx, appID, opts)
		out <- result{dep: dep, err: err}
		if err != nil {
			switch {
			case errors.Is(err, deploy.ErrDeployInProgress),
				errors.Is(err, deploy.ErrTooManyDeploys):
				slog.Info("webhook: deploy rejected (back-pressure)",
					"app_id", appID, "reason", err.Error())
			default:
				slog.Warn("webhook: deploy failed",
					"app_id", appID, "err", err)
			}
		}
	}()
	// Wait briefly so the unit-test path (a fake Deployer that returns
	// instantly) can capture the row id. In production the deploy goroutine
	// races on the build which takes minutes, so we'd time out and return
	// nil — the caller copes by emitting just app_id.
	select {
	case r := <-out:
		return r.dep
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

// verifySignature returns true when sig matches HMAC-SHA256(secret, body)
// under GitHub's "sha256=<hex>" envelope. Empty secret OR empty header
// always returns false — we never want to accept unsigned deliveries.
func verifySignature(secret, header string, body []byte) bool {
	if secret == "" || header == "" {
		return false
	}
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	provided, err := hex.DecodeString(header[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(provided, expected)
}

// branchFromRef extracts the branch name from a refs/heads/<name> ref.
// Returns "" for tag pushes or any ref that doesn't carry a branch.
func branchFromRef(ref string) string {
	const prefix = "refs/heads/"
	if !strings.HasPrefix(ref, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(ref, prefix))
}

// repoURLMatches checks whether `stored` (whatever the operator typed
// when creating the app) refers to the same GitHub repository as one
// of the URLs GitHub sent in the push payload.
//
// We normalise away the `.git` suffix and trim whitespace. Anything
// more aggressive (case-folding, scheme conversion) risks accepting
// a push delivery for a different repo just because the URL textually
// resembles ours, so we leave the canonical-vs-mirror question to the
// operator.
func repoURLMatches(stored, cloneURL, sshURL string) bool {
	s := normaliseGitURL(stored)
	if s == "" {
		return false
	}
	if s == normaliseGitURL(cloneURL) {
		return true
	}
	if s == normaliseGitURL(sshURL) {
		return true
	}
	return false
}

func normaliseGitURL(u string) string {
	v := strings.TrimSpace(u)
	v = strings.TrimSuffix(v, ".git")
	return v
}

