package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/prexel/prexel/internal/cli/client"
	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// appDTO mirrors internal/app.App for JSON unmarshalling. Only the fields the
// CLI reads or writes are kept.
type appDTO struct {
	ID                     string            `json:"id"`
	Name                   string            `json:"name"`
	Description            *string           `json:"description,omitempty"`
	ServerID               *string           `json:"server_id,omitempty"`
	GitSourceID            *string           `json:"git_source_id,omitempty"`
	RepoURL                *string           `json:"repo_url,omitempty"`
	Branch                 string            `json:"branch"`
	BuildType              string            `json:"build_type"`
	DockerfilePath         string            `json:"dockerfile_path,omitempty"`
	BuildContext           string            `json:"build_context,omitempty"`
	DockerfileInline       *string           `json:"dockerfile_inline,omitempty"`
	ImageName              *string           `json:"image_name,omitempty"`
	ImageTag               string            `json:"image_tag"`
	Port                   *int              `json:"port,omitempty"`
	HostPort               *int              `json:"host_port,omitempty"`
	ContainerName          *string           `json:"container_name,omitempty"`
	HealthCheckEnabled     bool              `json:"health_check_enabled"`
	HealthCheckPath        string            `json:"health_check_path,omitempty"`
	HealthCheckMethod      string            `json:"health_check_method,omitempty"`
	HealthCheckReturnCode  int               `json:"health_check_return_code,omitempty"`
	HealthCheckInterval    int               `json:"health_check_interval,omitempty"`
	HealthCheckTimeout     int               `json:"health_check_timeout,omitempty"`
	HealthCheckRetries     int               `json:"health_check_retries,omitempty"`
	HealthCheckStartPeriod int               `json:"health_check_start_period,omitempty"`
	EnvVars                map[string]string `json:"env_vars,omitempty"`
	Status                 string            `json:"status"`
	CreatedAt              int64             `json:"created_at"`
	UpdatedAt              int64             `json:"updated_at"`
}

func newAppCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "app",
		Aliases: []string{"apps"},
		Short:   "Manage applications",
	}
	c.AddCommand(
		newAppListCmd(),
		newAppInfoCmd(),
		newAppCreateCmd(),
		newAppEditCmd(),
		newAppSetCmd(),
		newAppStopCmd(),
		newAppRestartCmd(),
		newAppRemoveCmd(),
		newAppStatsCmd(),
		newAppVolumeCmd(),
		newAppTagCmd(),
		newAppAutoDeployCmd(),
	)
	return c
}

// newAppSetCmd is a flag-based partial update — one PATCH per call.
// Designed for scripting (`prexel app set api --restart-policy=always`):
// YAML edit via `app edit` is friendlier for humans, but pipelines and
// Ansible-style runners need a single-shot, idempotent surface.
//
// Only flags that the user actually passes are included in the body — an
// unset flag is a true no-op. The single exception is the empty-string
// trick documented per-flag below: passing `--auto-deploy-branch=""`
// sends JSON `null` so the backend's ClearAutoDeployBranch path fires.
func newAppSetCmd() *cobra.Command {
	var (
		restartPolicy      string
		preDeploy          string
		postDeploy         string
		autoDeployBranch   string
		installCommand     string
		buildCommand       string
		startCommand       string
		memory             string
		cpu                string
		memorySwap         string
		memorySwappiness   int
		memoryReservation  string
		cpuset             string
		cpuShares          int
		buildArgsInject    bool
		buildArgsSrcCommit bool
		labels             []string
	)
	c := &cobra.Command{
		Use:   "set <name>",
		Short: "Atualiza campos de configuração do app (PATCH parcial via flags)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body := map[string]any{}
			flags := cmd.Flags()
			if flags.Changed("restart-policy") {
				body["restart_policy"] = restartPolicy
			}
			if flags.Changed("pre-deploy") {
				body["pre_deploy_command"] = preDeploy
			}
			if flags.Changed("post-deploy") {
				body["post_deploy_command"] = postDeploy
			}
			// auto-deploy-branch: empty string clears the field (sent as
			// JSON null so the backend's ClearAutoDeployBranch path fires).
			if flags.Changed("auto-deploy-branch") {
				if autoDeployBranch == "" {
					body["auto_deploy_branch"] = nil
				} else {
					body["auto_deploy_branch"] = autoDeployBranch
				}
			}
			if flags.Changed("install-command") {
				body["install_command"] = installCommand
			}
			if flags.Changed("build-command") {
				body["build_command"] = buildCommand
			}
			if flags.Changed("start-command") {
				body["start_command"] = startCommand
			}
			if flags.Changed("memory") {
				body["limits_memory"] = memory
			}
			if flags.Changed("cpu") {
				body["limits_cpus"] = cpu
			}
			if flags.Changed("memory-swap") {
				body["limits_memory_swap"] = memorySwap
			}
			if flags.Changed("memory-swappiness") {
				body["limits_memory_swappiness"] = memorySwappiness
			}
			if flags.Changed("memory-reservation") {
				body["limits_memory_reservation"] = memoryReservation
			}
			if flags.Changed("cpuset") {
				body["limits_cpuset"] = cpuset
			}
			if flags.Changed("cpu-shares") {
				body["limits_cpu_shares"] = cpuShares
			}
			if flags.Changed("build-args-inject") {
				body["build_args_inject"] = buildArgsInject
			}
			if flags.Changed("build-args-source-commit") {
				body["build_args_source_commit"] = buildArgsSrcCommit
			}
			if flags.Changed("label") {
				// Repeated --label key=value pairs become a map. Backend
				// PATCH semantics REPLACE the full map, matching `docker
				// run --label`'s additive feel from the CLI side (a fresh
				// `set --label` invocation always sends the explicit set).
				lm, err := parseKVList(labels)
				if err != nil {
					return err
				}
				body["docker_labels"] = lm
			}
			if len(body) == 0 {
				return errors.New("nada para atualizar — passe ao menos uma flag")
			}
			var out appDTO
			if err := c.Patch(cmd.Context(), "/api/v1/apps/"+a.ID, body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Atualizado %s (%d campo(s))\n", out.Name, len(body))
			return nil
		},
	}
	c.Flags().StringVar(&restartPolicy, "restart-policy", "", "Política de restart do container (no|always|on-failure|unless-stopped)")
	c.Flags().StringVar(&preDeploy, "pre-deploy", "", "Comando executado antes do deploy")
	c.Flags().StringVar(&postDeploy, "post-deploy", "", "Comando executado após o deploy")
	c.Flags().StringVar(&autoDeployBranch, "auto-deploy-branch", "", "Branch que dispara deploy automático via webhook (\"\" para limpar)")
	c.Flags().StringVar(&installCommand, "install-command", "", "Comando de instalação (nixpacks/buildpacks)")
	c.Flags().StringVar(&buildCommand, "build-command", "", "Comando de build")
	c.Flags().StringVar(&startCommand, "start-command", "", "Comando de start do container")
	c.Flags().StringVar(&memory, "memory", "", "Limite de memória (ex: 512m, 1g)")
	c.Flags().StringVar(&cpu, "cpu", "", "Limite de CPU (ex: 0.5, 2)")
	c.Flags().StringVar(&memorySwap, "memory-swap", "", "Limite de swap (ex: 1g)")
	c.Flags().IntVar(&memorySwappiness, "memory-swappiness", 0, "Swappiness (0-100)")
	c.Flags().StringVar(&memoryReservation, "memory-reservation", "", "Reserva soft de memória (ex: 256m)")
	c.Flags().StringVar(&cpuset, "cpuset", "", "CPUs permitidas (ex: 0,2-4)")
	c.Flags().IntVar(&cpuShares, "cpu-shares", 0, "Peso relativo de CPU")
	c.Flags().BoolVar(&buildArgsInject, "build-args-inject", false, "Injeta env vars como --build-arg")
	c.Flags().BoolVar(&buildArgsSrcCommit, "build-args-source-commit", false, "Injeta SOURCE_COMMIT como --build-arg")
	c.Flags().StringArrayVar(&labels, "label", nil, "Label docker (key=value, repetível). Substitui o conjunto atual")
	return c
}

// parseKVList turns ["k=v","k2=v2"] into a map. Empty value is allowed
// (a docker label `KEY=` is meaningful and distinct from KEY absent).
func parseKVList(items []string) (map[string]string, error) {
	out := map[string]string{}
	for _, kv := range items {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("formato esperado key=value, recebi %q", kv)
		}
		out[kv[:eq]] = kv[eq+1:]
	}
	return out, nil
}

// newAppAutoDeployCmd is a thin wrapper around `app set
// --auto-deploy-branch` for the "give me the toggle" UX. It also
// exposes a `status` subcommand because the auto-deploy state isn't
// printed in `app info` yet — operators end up grepping the JSON
// otherwise.
//
// Backend semantics: empty string clears the column (sent as JSON
// null). `on` requires --branch; `off` always sends null.
func newAppAutoDeployCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "auto-deploy <app> <on|off|status>",
		Short: "Liga/desliga o deploy automático por webhook",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := strings.ToLower(args[1])
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(cli, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			switch mode {
			case "on":
				branch, _ := cmd.Flags().GetString("branch")
				if branch == "" {
					return errors.New("--branch é obrigatório com `on`")
				}
				body := map[string]any{"auto_deploy_branch": branch}
				if err := cli.Patch(cmd.Context(), "/api/v1/apps/"+a.ID, body, nil); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Auto-deploy de %s ligado para branch %s\n", a.Name, branch)
				return nil
			case "off":
				body := map[string]any{"auto_deploy_branch": nil}
				if err := cli.Patch(cmd.Context(), "/api/v1/apps/"+a.ID, body, nil); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Auto-deploy de %s desligado\n", a.Name)
				return nil
			case "status":
				// Re-fetch para refletir o estado atual; tipa o campo
				// localmente porque appDTO não o expõe ainda (e não
				// quero acoplar o DTO geral a esta subcommand).
				var full struct {
					AutoDeployBranch *string `json:"auto_deploy_branch"`
				}
				if err := cli.Get(cmd.Context(), "/api/v1/apps/"+a.ID, &full); err != nil {
					return err
				}
				if full.AutoDeployBranch == nil || *full.AutoDeployBranch == "" {
					fmt.Fprintln(cmd.OutOrStdout(), "off")
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), *full.AutoDeployBranch)
				}
				return nil
			default:
				return fmt.Errorf("modo desconhecido %q (esperado on|off|status)", mode)
			}
		},
	}
	c.Flags().String("branch", "", "Branch que dispara deploy (obrigatório com `on`)")
	return c
}

func resolveApp(c *client.Client, ctx context.Context, ref string) (*appDTO, error) {
	var a appDTO
	if err := c.Get(ctx, "/api/v1/apps/"+ref, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// primaryDomainFor returns the primary domain for an app or "-".
func primaryDomainFor(c *client.Client, ctx context.Context, appID string) string {
	var doms []domainDTO
	if err := c.Get(ctx, "/api/v1/apps/"+appID+"/domains", &doms); err != nil {
		return "-"
	}
	for _, d := range doms {
		if d.IsPrimary {
			return d.Name
		}
	}
	if len(doms) > 0 {
		return doms[0].Name
	}
	return "-"
}

// lastDeployFor returns a short summary of the last deployment.
func lastDeployFor(c *client.Client, ctx context.Context, appID string) string {
	var deps []deploymentDTO
	if err := c.Get(ctx, "/api/v1/apps/"+appID+"/deployments?limit=1", &deps); err != nil || len(deps) == 0 {
		return "-"
	}
	d := deps[0]
	ts := fmtUnix(d.CreatedAt)
	return fmt.Sprintf("%s (%s)", d.Status, ts)
}

func newAppListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List apps",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			var apps []appDTO
			if err := c.Get(cmd.Context(), "/api/v1/apps", &apps); err != nil {
				return err
			}
			if len(apps) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no apps)")
				return nil
			}
			servers := map[string]string{}
			var sList []serverDTO
			if err := c.Get(cmd.Context(), "/api/v1/servers", &sList); err == nil {
				for _, s := range sList {
					servers[s.ID] = s.Name
				}
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "NAME\tSERVER\tSTATUS\tDOMAIN\tLAST DEPLOY")
			for _, a := range apps {
				srv := "-"
				if a.ServerID != nil {
					if n, ok := servers[*a.ServerID]; ok {
						srv = n
					}
				}
				dom := primaryDomainFor(c, cmd.Context(), a.ID)
				last := lastDeployFor(c, cmd.Context(), a.ID)
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					a.Name, srv, colorStatus(a.Status), dom, last)
			}
			return tw.Flush()
		},
	}
}

// colorStatus wraps a status string with ANSI color when running in a TTY.
// Keeps the wide-table layout the same length by stripping codes when piping.
func colorStatus(s string) string {
	if !tui.IsTTY() {
		return s
	}
	switch s {
	case "running":
		return "\033[32m" + s + "\033[0m"
	case "building", "deploying":
		return "\033[33m" + s + "\033[0m"
	case "error", "unreachable", "failed":
		return "\033[31m" + s + "\033[0m"
	default:
		return "\033[2m" + s + "\033[0m"
	}
}

func newAppInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <name>",
		Short: "Show detailed info for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "id:           %s\n", a.ID)
			fmt.Fprintf(w, "name:         %s\n", a.Name)
			if a.Description != nil {
				fmt.Fprintf(w, "description:  %s\n", *a.Description)
			}
			fmt.Fprintf(w, "server_id:    %s\n", strOrDash(a.ServerID))
			fmt.Fprintf(w, "build_type:   %s\n", a.BuildType)
			fmt.Fprintf(w, "image:        %s:%s\n", strOrDash(a.ImageName), a.ImageTag)
			fmt.Fprintf(w, "repo_url:     %s\n", strOrDash(a.RepoURL))
			fmt.Fprintf(w, "branch:       %s\n", a.Branch)
			fmt.Fprintf(w, "port:         %s\n", intOrDash(a.Port))
			fmt.Fprintf(w, "host_port:    %s\n", intOrDash(a.HostPort))
			fmt.Fprintf(w, "container:    %s\n", strOrDash(a.ContainerName))
			fmt.Fprintf(w, "status:       %s\n", a.Status)
			fmt.Fprintf(w, "primary domain: %s\n", primaryDomainFor(c, cmd.Context(), a.ID))
			fmt.Fprintf(w, "last deploy:  %s\n", lastDeployFor(c, cmd.Context(), a.ID))
			return nil
		},
	}
}

func newAppCreateCmd() *cobra.Command {
	var (
		flagName        string
		flagServer      string
		flagBuildType   string
		flagRepoURL     string
		flagBranch      string
		flagImageName   string
		flagImageTag    string
		flagPort        int
		flagDescription string
	)
	c := &cobra.Command{
		Use:   "create",
		Short: "Create a new app",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			interactive := flagName == "" && flagServer == ""
			if interactive {
				if !tui.IsStdinTTY() {
					return errors.New("interactive `app create` requires a TTY; pass --name/--server")
				}
				if flagName, err = tui.Prompt("App name: "); err != nil {
					return err
				}
				if flagServer, err = tui.Prompt("Server (name): "); err != nil {
					return err
				}
				if flagBuildType, err = tui.PromptDefault("Build type (dockerfile/docker_image)", "docker_image"); err != nil {
					return err
				}
				if flagBuildType == "docker_image" {
					if flagImageName, err = tui.Prompt("Image name (e.g. nginx): "); err != nil {
						return err
					}
					if flagImageTag, err = tui.PromptDefault("Image tag", "latest"); err != nil {
						return err
					}
				} else {
					if flagRepoURL, err = tui.Prompt("Repo URL: "); err != nil {
						return err
					}
					if flagBranch, err = tui.PromptDefault("Branch", "main"); err != nil {
						return err
					}
				}
				portS, err := tui.PromptDefault("Port", "80")
				if err != nil {
					return err
				}
				_, _ = fmt.Sscanf(portS, "%d", &flagPort)
			}
			srv, err := resolveServer(c, cmd.Context(), flagServer)
			if err != nil {
				return fmt.Errorf("resolve server: %w", err)
			}
			body := map[string]any{
				"name":        flagName,
				"description": flagDescription,
				"server_id":   srv.ID,
				"build_type":  flagBuildType,
				"branch":      defaultStr(flagBranch, "main"),
				"image_tag":   defaultStr(flagImageTag, "latest"),
			}
			if flagRepoURL != "" {
				body["repo_url"] = flagRepoURL
			}
			if flagImageName != "" {
				body["image_name"] = flagImageName
			}
			if flagPort > 0 {
				body["port"] = flagPort
			}
			var out appDTO
			if err := c.Post(cmd.Context(), "/api/v1/apps", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created app %s (%s)\n", out.Name, out.ID)
			return nil
		},
	}
	c.Flags().StringVar(&flagName, "name", "", "App name")
	c.Flags().StringVar(&flagServer, "server", "", "Target server name or id")
	c.Flags().StringVar(&flagBuildType, "build-type", "docker_image", "dockerfile | docker_image")
	c.Flags().StringVar(&flagRepoURL, "repo-url", "", "Git repository URL")
	c.Flags().StringVar(&flagBranch, "branch", "main", "Git branch")
	c.Flags().StringVar(&flagImageName, "image-name", "", "Docker image name (docker_image build)")
	c.Flags().StringVar(&flagImageTag, "image-tag", "latest", "Docker image tag")
	c.Flags().IntVar(&flagPort, "port", 0, "Container port to expose")
	c.Flags().StringVar(&flagDescription, "description", "", "Free-form description")
	return c
}

func defaultStr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func newAppEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit <name>",
		Short: "Edit an app's settings in $EDITOR (YAML)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			tmp, err := os.CreateTemp("", "prexel-app-*.yaml")
			if err != nil {
				return err
			}
			defer func() { _ = os.Remove(tmp.Name()) }()

			// Build an editable subset to avoid the user breaking generated fields.
			editable := map[string]any{
				"description":      a.Description,
				"branch":           a.Branch,
				"image_name":       a.ImageName,
				"image_tag":        a.ImageTag,
				"port":             a.Port,
				"host_port":        a.HostPort,
				"dockerfile_path":  a.DockerfilePath,
				"build_context":    a.BuildContext,
				"env_vars":         a.EnvVars,
			}
			enc := yaml.NewEncoder(tmp)
			enc.SetIndent(2)
			if err := enc.Encode(editable); err != nil {
				return err
			}
			_ = enc.Close()
			_ = tmp.Close()

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			ec := exec.Command(editor, tmp.Name())
			ec.Stdin = os.Stdin
			ec.Stdout = os.Stdout
			ec.Stderr = os.Stderr
			if err := ec.Run(); err != nil {
				return fmt.Errorf("editor: %w", err)
			}
			data, err := os.ReadFile(tmp.Name())
			if err != nil {
				return err
			}
			var patch map[string]any
			if err := yaml.Unmarshal(data, &patch); err != nil {
				return fmt.Errorf("parse yaml: %w", err)
			}
			var updated appDTO
			if err := c.Patch(cmd.Context(), "/api/v1/apps/"+a.ID, patch, &updated); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated %s\n", updated.Name)
			return nil
		},
	}
}

func newAppStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop the running container for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.Post(cmd.Context(), "/api/v1/apps/"+a.ID+"/stop", nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Stopped %s\n", a.Name)
			return nil
		},
	}
}

func newAppRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <name>",
		Short: "Restart the running container for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.Post(cmd.Context(), "/api/v1/apps/"+a.ID+"/restart", nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Restarted %s\n", a.Name)
			return nil
		},
	}
}

func newAppRemoveCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove an app",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !force && !tui.Confirm(fmt.Sprintf("Remove app %s and its container?", a.Name)) {
				return errors.New("cancelled")
			}
			if err := c.Delete(cmd.Context(), "/api/v1/apps/"+a.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", a.Name)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Skip confirmation")
	return c
}

// Discover .prexel/config.yaml in the current directory (B3).
type prexelLocalCfg struct {
	App string `yaml:"app"`
}

// loadLocalAppName looks for `.prexel/config.yaml` in cwd → parents (up to 4
// levels) and returns its `app:` field. Returns ("", false) on miss.
func loadLocalAppName() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for i := 0; i < 5 && dir != "/"; i++ {
		path := filepath.Join(dir, ".prexel", "config.yaml")
		if data, err := os.ReadFile(path); err == nil {
			var c prexelLocalCfg
			if yaml.Unmarshal(data, &c) == nil && strings.TrimSpace(c.App) != "" {
				return c.App, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// statsDTO mirrors handler.containerStatsResp. Kept in the CLI
// package so we don't pull the server handler types into the client.
type statsDTO struct {
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryUsedBytes  uint64  `json:"memory_used_bytes"`
	MemoryLimitBytes uint64  `json:"memory_limit_bytes"`
	SampledAt        string  `json:"sampled_at"`
}

// newAppStatsCmd hits GET /apps/:id/stats. Backend takes ~1s to
// sample twice; with --watch the CLI polls every 5s until Ctrl+C.
func newAppStatsCmd() *cobra.Command {
	var watch bool
	c := &cobra.Command{
		Use:   "stats <name>",
		Short: "Show live CPU / memory usage for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			path := "/api/v1/apps/" + a.ID + "/stats"
			print := func() error {
				var s statsDTO
				if err := c.Get(cmd.Context(), path, &s); err != nil {
					return fmt.Errorf("stats: %w", err)
				}
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "cpu:       %.1f%%\n", s.CPUPercent)
				if s.MemoryLimitBytes > 0 {
					pct := float64(s.MemoryUsedBytes) / float64(s.MemoryLimitBytes) * 100
					fmt.Fprintf(w, "memory:    %s / %s (%.1f%%)\n",
						humanBytes(s.MemoryUsedBytes), humanBytes(s.MemoryLimitBytes), pct)
				} else {
					fmt.Fprintf(w, "memory:    %s / sem limite\n", humanBytes(s.MemoryUsedBytes))
				}
				fmt.Fprintf(w, "sampled:   %s\n", s.SampledAt)
				return nil
			}
			if !watch {
				return print()
			}
			// --watch loop. Same 5s cadence as the web UI; honors
			// ctx cancellation so Ctrl+C exits cleanly.
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			if err := print(); err != nil {
				return err
			}
			for {
				select {
				case <-cmd.Context().Done():
					return nil
				case <-ticker.C:
					fmt.Fprintln(cmd.OutOrStdout(), "---")
					if err := print(); err != nil {
						return err
					}
				}
			}
		},
	}
	c.Flags().BoolVarP(&watch, "watch", "w", false, "Poll continuously every 5s")
	return c
}

func humanBytes(n uint64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/1024/1024/1024)
	}
}
