package command

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/prexel/prexel/internal/cli/client"
	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

type gitSourceDTO struct {
	ID             string  `json:"id"`
	Type           string  `json:"type"`
	Name           string  `json:"name"`
	InstallationID *string `json:"installation_id,omitempty"`
	PublicKey      *string `json:"public_key,omitempty"`
	HasToken       bool    `json:"has_token"`
	HasPrivateKey  bool    `json:"has_private_key"`
	// GitHub App identity — populated for type='github_app' (post-009).
	AppID          string `json:"app_id,omitempty"`
	AppSlug        string `json:"app_slug,omitempty"`
	AccountLogin   string `json:"account_login,omitempty"`
	AccountType    string `json:"account_type,omitempty"`
	PendingInstall bool   `json:"pending_install,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type gitSourceListResp struct {
	Items []gitSourceDTO `json:"items"`
}

func newGitSourcesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "git-sources",
		Aliases: []string{"git-source"},
		Short:   "Manage git credentials (GitHub App, SSH key, PAT)",
	}
	c.AddCommand(
		newGitSourcesListCmd(),
		newGitSourcesAddCmd(),
		newGitSourcesRemoveCmd(),
		newGitSourcesTestCmd(),
		newGitSourcesReposCmd(),
		newGitSourcesBranchesCmd(),
		newGitSourcesInspectCmd(),
		newGitSourcesRegenerateWebhookCmd(),
	)
	return c
}

func newGitSourcesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List git sources",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			var out gitSourceListResp
			if err := c.Get(cmd.Context(), "/api/v1/git-sources", &out); err != nil {
				return err
			}
			if len(out.Items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no git sources)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "NAME\tTYPE\tACCOUNT\tDETAILS")
			for _, s := range out.Items {
				account := "-"
				details := ""
				switch s.Type {
				case "github_app":
					// Post-009: prefer the resolved account_login
					// over the opaque installation_id, mirroring the
					// new UI affordance. Pending rows are flagged
					// explicitly so the operator can tell why a
					// listing/clone might 4xx on them.
					if s.AccountLogin != "" {
						account = s.AccountLogin
						if s.AccountType != "" {
							account += " (" + s.AccountType + ")"
						}
					}
					if s.PendingInstall {
						details = "install pending"
					} else if s.AppSlug != "" {
						details = "app=" + s.AppSlug
					}
				case "ssh_key":
					details = fmt.Sprintf("has_key=%v", s.HasPrivateKey)
				case "token":
					details = fmt.Sprintf("has_token=%v", s.HasToken)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, s.Type, account, details)
			}
			return tw.Flush()
		},
	}
}

func newGitSourcesAddCmd() *cobra.Command {
	var (
		name       string
		generate   bool
		token      string
		privateKey string
		scope      string
		org        string
	)
	c := &cobra.Command{
		Use:   "add <type>",
		Short: "Add a git source (type: github|ssh-key|token)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			typ := args[0]
			switch typ {
			case "github":
				// Self-contained OAuth flow — CLI binds a localhost
				// port, drives the GitHub manifest dance itself, and
				// only talks to the Prexel REST API. Per CLAUDE.md,
				// the CLI must never depend on the Prexel web UI to
				// complete an operation. See githubapp_local.go.
				scope = strings.TrimSpace(strings.ToLower(scope))
				if scope == "" {
					scope = "personal"
				}
				if scope != "personal" && scope != "org" {
					return fmt.Errorf("--scope must be 'personal' or 'org', got %q", scope)
				}
				if scope == "org" && strings.TrimSpace(org) == "" {
					return errors.New("--org is required when --scope=org")
				}
				if scope == "personal" {
					org = ""
				}
				logf := func(format string, args ...any) {
					fmt.Fprintln(cmd.OutOrStdout(), fmt.Sprintf(format, args...))
				}
				src, err := runLocalGitHubAppFlow(cmd.Context(), c, scope, org, name, logf)
				if err != nil {
					return fmt.Errorf("github flow: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(),
					"\nConectado: %s (id=%s)\n", displayAccount(src), src.ID)
				return nil
			case "ssh-key":
				if name == "" {
					return errors.New("--name is required")
				}
				if !generate && privateKey == "" {
					return errors.New("either --generate or --private-key is required")
				}
				return postSource(c, cmd.Context(), map[string]any{
					"type":        "ssh_key",
					"name":        name,
					"generate":    generate,
					"private_key": privateKey,
				})
			case "token":
				if name == "" || token == "" {
					return errors.New("--name and --token are required")
				}
				return postSource(c, cmd.Context(), map[string]any{
					"type":  "token",
					"name":  name,
					"token": token,
				})
			default:
				return fmt.Errorf("unknown git source type: %q (expected github|ssh-key|token)", typ)
			}
		},
	}
	c.Flags().StringVar(&name, "name", "", "Display name")
	c.Flags().BoolVar(&generate, "generate", false, "Generate Ed25519 SSH keypair (ssh-key)")
	c.Flags().StringVar(&token, "token", "", "Personal access token (token)")
	c.Flags().StringVar(&privateKey, "private-key", "", "Pasted private key PEM (ssh-key, alt. to --generate)")
	c.Flags().StringVar(&scope, "scope", "personal", "GitHub scope: personal|org (github)")
	c.Flags().StringVar(&org, "org", "", "GitHub org slug (required when --scope=org)")
	return c
}

func postSource(c *client.Client, ctx context.Context, body map[string]any) error {
	var out gitSourceDTO
	if err := c.Post(ctx, "/api/v1/git-sources", body, &out); err != nil {
		return err
	}
	fmt.Printf("Created git source %s (%s)\n", out.Name, out.ID)
	if out.PublicKey != nil && *out.PublicKey != "" {
		fmt.Println("\nAdd this key as a deploy key on the target repos:")
		fmt.Println(*out.PublicKey)
	}
	return nil
}

func newGitSourcesRemoveCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a git source",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			s, err := resolveGitSource(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !force && !tui.Confirm(fmt.Sprintf("Remove git source %s?", s.Name)) {
				return errors.New("cancelled")
			}
			if err := c.Delete(cmd.Context(), "/api/v1/git-sources/"+s.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", s.Name)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Skip confirmation")
	return c
}

func newGitSourcesTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test <name> <repo_url>",
		Short: "Test git source credentials against a repo URL",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			s, err := resolveGitSource(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var out map[string]any
			err = tui.RunSpinner("Testing git source", func() error {
				return c.Post(cmd.Context(), "/api/v1/git-sources/"+s.ID+"/test",
					map[string]string{"repo_url": args[1]}, &out)
			})
			if err != nil {
				return err
			}
			if ok, _ := out["ok"].(bool); ok {
				fmt.Fprintln(cmd.OutOrStdout(), "ok")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "failed: %v\n", out["error"])
			return errors.New("test failed")
		},
	}
}

func resolveGitSource(c *client.Client, ctx context.Context, ref string) (*gitSourceDTO, error) {
	var out gitSourceListResp
	if err := c.Get(ctx, "/api/v1/git-sources", &out); err != nil {
		return nil, err
	}
	for i := range out.Items {
		if out.Items[i].ID == ref || out.Items[i].Name == ref {
			return &out.Items[i], nil
		}
	}
	return nil, fmt.Errorf("git source %q not found", ref)
}

type gitRepoDTO struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	FullName      string  `json:"full_name"`
	Private       bool    `json:"private"`
	HTMLURL       string  `json:"html_url"`
	CloneURL      string  `json:"clone_url"`
	DefaultBranch string  `json:"default_branch"`
	Language      *string `json:"language,omitempty"`
	UpdatedAt     *string `json:"updated_at,omitempty"`
}

type gitRepoListResp struct {
	Items []gitRepoDTO `json:"items"`
	Error string       `json:"error,omitempty"`
}

type gitBranchDTO struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

type gitBranchListResp struct {
	Items []gitBranchDTO `json:"items"`
	Error string         `json:"error,omitempty"`
}

type gitInspectResp struct {
	RepoURL        string   `json:"repo_url"`
	Branch         string   `json:"branch"`
	DockerfilePath *string  `json:"dockerfile_path,omitempty"`
	ComposeFile    *string  `json:"compose_file,omitempty"`
	BuildContext   string   `json:"build_context"`
	SuggestedType  string   `json:"suggested_type"`
	FoundFiles     []string `json:"found_files"`
	OK             *bool    `json:"ok,omitempty"`
	Error          string   `json:"error,omitempty"`
}

func newGitSourcesReposCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "repos <source-name>",
		Short: "List repositories accessible to a git source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			s, err := resolveGitSource(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var out gitRepoListResp
			if err := c.Get(cmd.Context(), "/api/v1/git-sources/"+s.ID+"/repositories", &out); err != nil {
				return err
			}
			if out.Error != "" {
				return fmt.Errorf("git provider: %s", out.Error)
			}
			if len(out.Items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no repositories)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "REPO\tDEFAULT BRANCH\tPRIVATE\tUPDATED")
			for _, r := range out.Items {
				priv := "✗"
				if r.Private {
					priv = "✓"
				}
				upd := "-"
				if r.UpdatedAt != nil && *r.UpdatedAt != "" {
					upd = *r.UpdatedAt
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.FullName, r.DefaultBranch, priv, upd)
			}
			return tw.Flush()
		},
	}
}

func newGitSourcesBranchesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "branches <source-name> <owner> <repo>",
		Short: "List branches of a repository",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			s, err := resolveGitSource(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			owner, repo := args[1], args[2]
			var out gitBranchListResp
			path := fmt.Sprintf("/api/v1/git-sources/%s/repositories/%s/%s/branches", s.ID, owner, repo)
			if err := c.Get(cmd.Context(), path, &out); err != nil {
				return err
			}
			if out.Error != "" {
				return fmt.Errorf("git provider: %s", out.Error)
			}
			if len(out.Items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no branches)")
				return nil
			}
			// Try to learn the default branch so we can flag it. Best-effort.
			defaultBranch := defaultBranchFor(c, cmd.Context(), s.ID, owner, repo)
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "BRANCH\tCOMMIT\tDEFAULT")
			for _, b := range out.Items {
				short := b.SHA
				if len(short) > 7 {
					short = short[:7]
				}
				def := "✗"
				if defaultBranch != "" && b.Name == defaultBranch {
					def = "✓"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", b.Name, short, def)
			}
			return tw.Flush()
		},
	}
}

// defaultBranchFor returns the default branch for owner/repo, or "" on failure.
// Used only to decorate the branches table — never fatal.
func defaultBranchFor(c *client.Client, ctx context.Context, sourceID, owner, repo string) string {
	var out gitRepoListResp
	if err := c.Get(ctx, "/api/v1/git-sources/"+sourceID+"/repositories", &out); err != nil {
		return ""
	}
	want := owner + "/" + repo
	for _, r := range out.Items {
		if r.FullName == want {
			return r.DefaultBranch
		}
	}
	return ""
}

func newGitSourcesInspectCmd() *cobra.Command {
	var branch string
	c := &cobra.Command{
		Use:   "inspect <source-name> <owner/repo>",
		Short: "Inspect a repository for Dockerfile/compose and suggested build type",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			s, err := resolveGitSource(cli, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			parts := strings.SplitN(args[1], "/", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return errors.New("expected <owner/repo>")
			}
			body := map[string]string{
				"owner": parts[0],
				"repo":  parts[1],
			}
			if strings.TrimSpace(branch) != "" {
				body["branch"] = strings.TrimSpace(branch)
			}
			var out gitInspectResp
			if err := cli.Post(cmd.Context(), "/api/v1/git-sources/"+s.ID+"/repositories/inspect", body, &out); err != nil {
				return err
			}
			if out.OK != nil && !*out.OK {
				return fmt.Errorf("inspect failed: %s", out.Error)
			}
			if out.Error != "" {
				return fmt.Errorf("inspect failed: %s", out.Error)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "repo_url:        %s\n", out.RepoURL)
			fmt.Fprintf(w, "branch:          %s\n", out.Branch)
			fmt.Fprintf(w, "build_context:   %s\n", out.BuildContext)
			fmt.Fprintf(w, "suggested_type:  %s\n", out.SuggestedType)
			fmt.Fprintf(w, "dockerfile:      %s\n", strOrDash(out.DockerfilePath))
			fmt.Fprintf(w, "compose_file:    %s\n", strOrDash(out.ComposeFile))
			if len(out.FoundFiles) > 0 {
				fmt.Fprintf(w, "found_files:     %s\n", strings.Join(out.FoundFiles, ", "))
			} else {
				fmt.Fprintln(w, "found_files:     -")
			}
			return nil
		},
	}
	c.Flags().StringVar(&branch, "branch", "", "Branch to inspect (defaults to main on the server)")
	return c
}

// openBrowser tries to open the URL in the user's default browser. Best-effort.
func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}
