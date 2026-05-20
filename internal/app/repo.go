// Package app is the domain layer for "apps" — the deployable workloads
// managed by Prexel. It owns CRUD over the `apps` table and (in A8) drives the
// deploy engine. Today the package only persists configuration; the actual
// container lifecycle lands with the Deploy Engine milestone.
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned when a lookup misses.
var ErrNotFound = errors.New("app: not found")

// ErrNameInUse is returned when the unique constraint on name trips.
var ErrNameInUse = errors.New("app: name already in use")

// App is the API/service-facing representation of a row in `apps`.
type App struct {
	ID                      string            `json:"id"`
	Name                    string            `json:"name"`
	Description             *string           `json:"description,omitempty"`
	TeamID                  *string           `json:"team_id,omitempty"`
	ServerID                *string           `json:"server_id,omitempty"`
	GitSourceID             *string           `json:"git_source_id,omitempty"`
	RepoURL                 *string           `json:"repo_url,omitempty"`
	Branch                  string            `json:"branch"`
	GitCommitSHA            *string           `json:"git_commit_sha,omitempty"`
	BuildType               string            `json:"build_type"`
	DockerfilePath          string            `json:"dockerfile_path"`
	BuildContext            string            `json:"build_context"`
	DockerfileInline        *string           `json:"dockerfile_inline,omitempty"`
	ComposeFile             *string           `json:"compose_file,omitempty"`
	ComposeInline           *string           `json:"compose_inline,omitempty"`
	ImageName               *string           `json:"image_name,omitempty"`
	ImageTag                string            `json:"image_tag"`
	InstallCommand          *string           `json:"install_command,omitempty"`
	BuildCommand            *string           `json:"build_command,omitempty"`
	StartCommand            *string           `json:"start_command,omitempty"`
	PreDeployCommand        *string           `json:"pre_deploy_command,omitempty"`
	PostDeployCommand       *string           `json:"post_deploy_command,omitempty"`
	Port                    *int              `json:"port,omitempty"`
	HostPort                *int              `json:"host_port,omitempty"`
	ContainerName           *string           `json:"container_name,omitempty"`
	HealthCheckEnabled      bool              `json:"health_check_enabled"`
	HealthCheckPath         string            `json:"health_check_path"`
	HealthCheckMethod       string            `json:"health_check_method"`
	HealthCheckPort         *int              `json:"health_check_port,omitempty"`
	HealthCheckReturnCode   int               `json:"health_check_return_code"`
	HealthCheckInterval     int               `json:"health_check_interval"`
	HealthCheckTimeout      int               `json:"health_check_timeout"`
	HealthCheckRetries      int               `json:"health_check_retries"`
	HealthCheckStartPeriod  int               `json:"health_check_start_period"`
	LimitsMemory            *string           `json:"limits_memory,omitempty"`
	LimitsCPUs              *string           `json:"limits_cpus,omitempty"`
	LimitsMemorySwap        *string           `json:"limits_memory_swap,omitempty"`
	LimitsMemorySwappiness  *int              `json:"limits_memory_swappiness,omitempty"`
	LimitsMemoryReservation *string           `json:"limits_memory_reservation,omitempty"`
	LimitsCPUSet            *string           `json:"limits_cpuset,omitempty"`
	LimitsCPUShares         *int              `json:"limits_cpu_shares,omitempty"`
	RestartPolicy           string            `json:"restart_policy"`
	AutoDeployBranch        *string           `json:"auto_deploy_branch,omitempty"`
	BuildArgsInject         bool              `json:"build_args_inject"`
	BuildArgsSourceCommit   bool              `json:"build_args_source_commit"`
	DockerLabels            map[string]string `json:"docker_labels"`
	EnvVars                 map[string]string `json:"env_vars"`
	Status                  string            `json:"status"`
	CreatedAt               int64             `json:"created_at"`
	UpdatedAt               int64             `json:"updated_at"`
	// Tags carries the names of attached tags. Populated lazily by
	// Service.Get / Service.List when a TagLister is wired in via
	// WithTags. Lives outside the apps table — fetched from app_tags
	// + tags via the tag service. Always present in JSON ("tags":[])
	// so the front-end never has to null-check.
	Tags []string `json:"tags"`
}

type repo struct {
	db *sql.DB
}

func newRepo(db *sql.DB) *repo { return &repo{db: db} }

// insert writes a freshly-built app row.
func (r *repo) insert(ctx context.Context, a *App) error {
	envJSON, err := json.Marshal(a.EnvVars)
	if err != nil {
		return fmt.Errorf("marshal env_vars: %w", err)
	}
	if a.DockerLabels == nil {
		a.DockerLabels = map[string]string{}
	}
	labelsJSON, err := json.Marshal(a.DockerLabels)
	if err != nil {
		return fmt.Errorf("marshal docker_labels: %w", err)
	}
	if a.RestartPolicy == "" {
		a.RestartPolicy = "unless-stopped"
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO apps(
			id, name, description, team_id, server_id, git_source_id, repo_url, branch, git_commit_sha,
			build_type, dockerfile_path, build_context, dockerfile_inline, compose_file, compose_inline,
			image_name, image_tag, install_command, build_command, start_command,
			pre_deploy_command, post_deploy_command,
			port, host_port, container_name,
			health_check_enabled, health_check_path, health_check_method, health_check_port,
			health_check_return_code, health_check_interval, health_check_timeout,
			health_check_retries, health_check_start_period,
			limits_memory, limits_cpus,
			limits_memory_swap, limits_memory_swappiness, limits_memory_reservation,
			limits_cpuset, limits_cpu_shares,
			restart_policy, auto_deploy_branch,
			build_args_inject, build_args_source_commit,
			docker_labels,
			env_vars, status
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?,
			?, ?,
			?, ?, ?,
			?, ?,
			?, ?,
			?, ?,
			?,
			?, ?
		)`,
		a.ID, a.Name, a.Description, a.TeamID, a.ServerID, a.GitSourceID, a.RepoURL, a.Branch, a.GitCommitSHA,
		a.BuildType, a.DockerfilePath, a.BuildContext, a.DockerfileInline, a.ComposeFile, a.ComposeInline,
		a.ImageName, a.ImageTag, a.InstallCommand, a.BuildCommand, a.StartCommand,
		a.PreDeployCommand, a.PostDeployCommand,
		a.Port, a.HostPort, a.ContainerName,
		boolToInt(a.HealthCheckEnabled), a.HealthCheckPath, a.HealthCheckMethod, a.HealthCheckPort,
		a.HealthCheckReturnCode, a.HealthCheckInterval, a.HealthCheckTimeout,
		a.HealthCheckRetries, a.HealthCheckStartPeriod,
		a.LimitsMemory, a.LimitsCPUs,
		a.LimitsMemorySwap, a.LimitsMemorySwappiness, a.LimitsMemoryReservation,
		a.LimitsCPUSet, a.LimitsCPUShares,
		a.RestartPolicy, a.AutoDeployBranch,
		boolToInt(a.BuildArgsInject), boolToInt(a.BuildArgsSourceCommit),
		string(labelsJSON),
		string(envJSON), a.Status,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrNameInUse
		}
		return err
	}
	return nil
}

func (r *repo) get(ctx context.Context, id string) (*App, error) {
	row := r.db.QueryRowContext(ctx, selectColumns+` FROM apps WHERE id = ?`, id)
	return scanApp(row)
}

func (r *repo) getByName(ctx context.Context, name string) (*App, error) {
	row := r.db.QueryRowContext(ctx, selectColumns+` FROM apps WHERE name = ?`, name)
	return scanApp(row)
}

func (r *repo) list(ctx context.Context, serverID string) ([]App, error) {
	q := selectColumns + ` FROM apps`
	args := []any{}
	if serverID != "" {
		q += ` WHERE server_id = ?`
		args = append(args, serverID)
	}
	q += ` ORDER BY created_at ASC`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (r *repo) delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM apps WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// updateStatus persists a status transition without touching anything else.
func (r *repo) updateStatus(ctx context.Context, id, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE apps SET status = ?, updated_at = unixepoch() WHERE id = ?`,
		status, id)
	return err
}

// updateEnvVars replaces the env_vars JSON document for the given app.
func (r *repo) updateEnvVars(ctx context.Context, id string, env map[string]string) error {
	b, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal env_vars: %w", err)
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE apps SET env_vars = ?, updated_at = unixepoch() WHERE id = ?`,
		string(b), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// updateFields runs a partial UPDATE built from the non-empty entries in `sets`.
// The map keys are SQL column names trusted by the caller (service layer).
func (r *repo) updateFields(ctx context.Context, id string, sets map[string]any) (*App, error) {
	if len(sets) == 0 {
		return r.get(ctx, id)
	}
	cols := make([]string, 0, len(sets))
	args := make([]any, 0, len(sets)+1)
	for col, v := range sets {
		cols = append(cols, col+" = ?")
		args = append(args, v)
	}
	args = append(args, id)
	q := fmt.Sprintf(`UPDATE apps SET %s, updated_at = unixepoch() WHERE id = ?`,
		strings.Join(cols, ", "))
	res, err := r.db.ExecContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return r.get(ctx, id)
}

const selectColumns = `SELECT
	id, name, description, team_id, server_id, git_source_id, repo_url, branch, git_commit_sha,
	build_type, dockerfile_path, build_context, dockerfile_inline, compose_file, compose_inline,
	image_name, image_tag, install_command, build_command, start_command,
	pre_deploy_command, post_deploy_command,
	port, host_port, container_name,
	health_check_enabled, health_check_path, health_check_method, health_check_port,
	health_check_return_code, health_check_interval, health_check_timeout,
	health_check_retries, health_check_start_period,
	limits_memory, limits_cpus,
	limits_memory_swap, limits_memory_swappiness, limits_memory_reservation,
	limits_cpuset, limits_cpu_shares,
	restart_policy, auto_deploy_branch,
	build_args_inject, build_args_source_commit,
	docker_labels,
	env_vars, status,
	created_at, updated_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanApp(s scanner) (*App, error) {
	var (
		a                                                                 App
		description, teamID, serverID, gitSourceID, repoURL, gitCommitSHA sql.NullString
		dockerfileInline, composeFile, composeInline, imageName           sql.NullString
		installCmd, buildCmd, startCmd, containerName                     sql.NullString
		preDeployCmd, postDeployCmd                                       sql.NullString
		limitsMemory, limitsCPUs                                          sql.NullString
		limitsMemorySwap, limitsMemoryReservation, limitsCPUSet           sql.NullString
		limitsMemorySwappiness, limitsCPUShares                           sql.NullInt64
		autoDeployBranch                                                  sql.NullString
		port, hostPort, healthCheckPort                                   sql.NullInt64
		healthCheckEnabled, buildArgsInject, buildArgsSourceCommit        int
		labelsJSON, envJSON                                               string
	)
	err := s.Scan(
		&a.ID, &a.Name, &description, &teamID, &serverID, &gitSourceID, &repoURL, &a.Branch, &gitCommitSHA,
		&a.BuildType, &a.DockerfilePath, &a.BuildContext, &dockerfileInline, &composeFile, &composeInline,
		&imageName, &a.ImageTag, &installCmd, &buildCmd, &startCmd,
		&preDeployCmd, &postDeployCmd,
		&port, &hostPort, &containerName,
		&healthCheckEnabled, &a.HealthCheckPath, &a.HealthCheckMethod, &healthCheckPort,
		&a.HealthCheckReturnCode, &a.HealthCheckInterval, &a.HealthCheckTimeout,
		&a.HealthCheckRetries, &a.HealthCheckStartPeriod,
		&limitsMemory, &limitsCPUs,
		&limitsMemorySwap, &limitsMemorySwappiness, &limitsMemoryReservation,
		&limitsCPUSet, &limitsCPUShares,
		&a.RestartPolicy, &autoDeployBranch,
		&buildArgsInject, &buildArgsSourceCommit,
		&labelsJSON,
		&envJSON, &a.Status,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Description = nullStr(description)
	a.TeamID = nullStr(teamID)
	a.ServerID = nullStr(serverID)
	a.GitSourceID = nullStr(gitSourceID)
	a.RepoURL = nullStr(repoURL)
	a.GitCommitSHA = nullStr(gitCommitSHA)
	a.DockerfileInline = nullStr(dockerfileInline)
	a.ComposeFile = nullStr(composeFile)
	a.ComposeInline = nullStr(composeInline)
	a.ImageName = nullStr(imageName)
	a.InstallCommand = nullStr(installCmd)
	a.BuildCommand = nullStr(buildCmd)
	a.StartCommand = nullStr(startCmd)
	a.PreDeployCommand = nullStr(preDeployCmd)
	a.PostDeployCommand = nullStr(postDeployCmd)
	a.ContainerName = nullStr(containerName)
	a.LimitsMemory = nullStr(limitsMemory)
	a.LimitsCPUs = nullStr(limitsCPUs)
	a.LimitsMemorySwap = nullStr(limitsMemorySwap)
	a.LimitsMemoryReservation = nullStr(limitsMemoryReservation)
	a.LimitsCPUSet = nullStr(limitsCPUSet)
	a.AutoDeployBranch = nullStr(autoDeployBranch)
	if limitsMemorySwappiness.Valid {
		v := int(limitsMemorySwappiness.Int64)
		a.LimitsMemorySwappiness = &v
	}
	if limitsCPUShares.Valid {
		v := int(limitsCPUShares.Int64)
		a.LimitsCPUShares = &v
	}
	if port.Valid {
		v := int(port.Int64)
		a.Port = &v
	}
	if hostPort.Valid {
		v := int(hostPort.Int64)
		a.HostPort = &v
	}
	if healthCheckPort.Valid {
		v := int(healthCheckPort.Int64)
		a.HealthCheckPort = &v
	}
	a.HealthCheckEnabled = healthCheckEnabled != 0
	a.BuildArgsInject = buildArgsInject != 0
	a.BuildArgsSourceCommit = buildArgsSourceCommit != 0
	a.EnvVars = map[string]string{}
	if envJSON != "" {
		if err := json.Unmarshal([]byte(envJSON), &a.EnvVars); err != nil {
			return nil, fmt.Errorf("parse env_vars: %w", err)
		}
	}
	a.DockerLabels = map[string]string{}
	if labelsJSON != "" {
		if err := json.Unmarshal([]byte(labelsJSON), &a.DockerLabels); err != nil {
			return nil, fmt.Errorf("parse docker_labels: %w", err)
		}
	}
	return &a, nil
}

func nullStr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
