package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/prexel/prexel/internal/cli/client"
	"github.com/spf13/cobra"
)

// deploymentDTO mirrors internal/deploy.Deployment.
type deploymentDTO struct {
	ID         string  `json:"id"`
	AppID      string  `json:"app_id"`
	CommitSHA  *string `json:"commit_sha,omitempty"`
	CommitMsg  *string `json:"commit_msg,omitempty"`
	Branch     *string `json:"branch,omitempty"`
	ImageTag   *string `json:"image_tag,omitempty"`
	RollbackOf *string `json:"rollback_of,omitempty"`
	Status     string  `json:"status"`
	LogPath    *string `json:"log_path,omitempty"`
	StartedAt  *int64  `json:"started_at,omitempty"`
	FinishedAt *int64  `json:"finished_at,omitempty"`
	CreatedAt  int64   `json:"created_at"`
}

func newDeployCmd() *cobra.Command {
	var (
		watch     bool
		branch    string
		tag       string
		commitSHA string
	)
	c := &cobra.Command{
		Use:   "deploy [app]",
		Short: "Trigger a deployment for an app",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			var appRef string
			if len(args) == 1 {
				appRef = args[0]
			} else {
				if name, ok := loadLocalAppName(); ok {
					appRef = name
				} else if name, ok := inferAppFromGit(); ok {
					appRef = name
				} else {
					return errors.New("no app name supplied and unable to infer one from .prexel/config.yaml or git remote")
				}
			}
			a, err := resolveApp(c, cmd.Context(), appRef)
			if err != nil {
				return err
			}
			body := map[string]string{}
			if branch != "" {
				body["branch"] = branch
			}
			if tag != "" {
				body["tag"] = tag
			}
			if commitSHA != "" {
				body["commit_sha"] = commitSHA
			}
			var resp map[string]any
			if err := c.Post(cmd.Context(), "/api/v1/apps/"+a.ID+"/deploy", body, &resp); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deploy queued for %s\n", a.Name)
			if !watch {
				return nil
			}
			return watchDeploy(cmd.Context(), c, a.ID)
		},
	}
	c.Flags().BoolVar(&watch, "watch", false, "Stream build/health/swap events")
	c.Flags().StringVar(&branch, "branch", "", "Override branch")
	c.Flags().StringVar(&tag, "tag", "", "Image tag (docker_image build)")
	c.Flags().StringVar(&commitSHA, "commit", "", "Specific commit SHA")
	return c
}

// inferAppFromGit returns the repo name from `git remote get-url origin` (last
// path component, .git stripped).
func inferAppFromGit() (string, bool) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", false
	}
	u := strings.TrimSpace(string(out))
	u = strings.TrimSuffix(u, ".git")
	base := filepath.Base(u)
	// `:user/repo` → trim everything before slash.
	if i := strings.LastIndex(base, ":"); i >= 0 {
		base = base[i+1:]
	}
	if base == "" || base == "." {
		return "", false
	}
	return base, true
}

// watchDeploy subscribes to /apps/{id}/events and prints lines until success/failed.
func watchDeploy(ctx context.Context, c *client.Client, appID string) error {
	ch, cancel, err := c.SSE(ctx, "/api/v1/apps/"+appID+"/events", "")
	if err != nil {
		return err
	}
	defer cancel()
	fmt.Fprintln(os.Stderr, "Watching events (Ctrl-C to detach)...")
	for ev := range ch {
		var payload struct {
			Type    string          `json:"type"`
			Topic   string          `json:"topic"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(ev.Data), &payload) != nil {
			continue
		}
		switch payload.Type {
		case "deploy.log":
			var p struct {
				Line  string `json:"line"`
				Phase string `json:"phase"`
			}
			_ = json.Unmarshal(payload.Payload, &p)
			if p.Phase != "" {
				fmt.Fprintf(os.Stderr, "[%s] %s\n", p.Phase, p.Line)
			} else {
				fmt.Fprintln(os.Stderr, p.Line)
			}
		case "deploy.started", "deploy.success", "deploy.failed", "deploy.phase":
			fmt.Fprintf(os.Stderr, "▶ %s\n", payload.Type)
			if payload.Type == "deploy.success" {
				return nil
			}
			if payload.Type == "deploy.failed" {
				return errors.New("deploy failed")
			}
		default:
			// ignore other topics
		}
	}
	return nil
}
