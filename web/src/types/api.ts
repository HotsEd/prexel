// Shared API types — kept thin and aligned with the Go structs in
// internal/{app,server,domain,deploy,secret}/repo.go. Only the fields the
// frontend actually consumes are listed; missing/extra fields are tolerated.

export interface SetupStatus {
  completed: boolean
  instance_id: string
  version: string
}

export interface LoginResponse {
  access_token: string
  expires_in: number
  token_type: string
}

export interface RefreshResponse {
  access_token: string
  expires_in: number
  token_type: string
}

/** When the backend wants a second factor before issuing a session. */
export interface TwoFactorRequiredResponse {
  requires_2fa: true
  challenge_id: string
  methods: TwoFactorMethod[]
}

export type TwoFactorMethod = 'app' | 'email' | 'recovery'

export interface User {
  id: string
  email: string
  name?: string
  avatar_url?: string
  status?: 'active' | 'inactive' | 'blocked'
  role?: string
  permissions?: string[]
  is_admin?: boolean
}

export interface PermissionDefinition {
  slug: string
  group: string
  name: string
  description: string
}

export interface Role {
  id: string
  slug: string
  name: string
  description: string
  scope: 'global' | 'team' | 'both'
  is_admin: boolean
  is_system: boolean
  permissions: string[]
  created_at: number
  updated_at: number
}

export interface Team {
  id: string
  name: string
  slug: string
  description?: string
  color: string
  avatar_url?: string
  member_count: number
  app_count: number
  created_at: number
  updated_at: number
}

export interface TeamDetail {
  team: Team
  members: Member[]
}

export interface Member {
  id: string
  email: string
  name?: string
  avatar_url?: string
  status: 'active' | 'inactive' | 'blocked'
  role?: Role
  team_role?: Role
  teams: Team[]
  created_at: number
}

export type ServerStatus = 'connected' | 'disconnected' | 'unknown' | 'pending'

export interface Server {
  id: string
  name: string
  type: 'local' | 'remote'
  host?: string | null
  port: number
  user?: string | null
  host_key_fingerprint?: string | null
  status: ServerStatus
  docker_version?: string | null
  last_checked_at?: number | null
  has_private_key: boolean
  public_key?: string
  created_at: number
  updated_at: number
}

export interface ServerCheck {
  name: string
  ok: boolean
  message?: string
}

export interface ServerTestReport {
  server_id: string
  type: string
  status: string
  checks: ServerCheck[]
}

export type AppStatus =
  | 'idle'
  | 'building'
  | 'deploying'
  | 'running'
  | 'stopped'
  | 'error'
  | 'unreachable'

/**
 * Container restart policy. Mirrors Docker's `--restart` values; the
 * backend rejects anything else (see internal/app/service.go validRestart).
 * Default at row insertion is `unless-stopped`.
 */
export type RestartPolicy = 'no' | 'always' | 'on-failure' | 'unless-stopped'

export interface App {
  id: string
  name: string
  description?: string | null
  team_id?: string | null
  server_id?: string | null
  git_source_id?: string | null
  repo_url?: string | null
  branch: string
  git_commit_sha?: string | null
  build_type: 'dockerfile' | 'docker_image' | 'docker_compose'
  dockerfile_path: string
  build_context: string
  dockerfile_inline?: string | null
  compose_file?: string | null
  compose_inline?: string | null
  image_name?: string | null
  image_tag: string
  install_command?: string | null
  build_command?: string | null
  start_command?: string | null
  /**
   * Pre-deploy hook — runs on the PREVIOUS container right before the
   * swap (typical use: `rails db:migrate`). Failure aborts the deploy.
   */
  pre_deploy_command?: string | null
  /**
   * Post-deploy hook — runs on the NEW container after the healthcheck
   * passes. Failure is logged but does not roll back; treat as a "warm
   * the cache" / "ping a webhook" slot, not a critical-path step.
   */
  post_deploy_command?: string | null
  port?: number | null
  host_port?: number | null
  container_name?: string | null
  health_check_enabled: boolean
  health_check_path: string
  health_check_method: string
  /**
   * Container resource limits. All optional — null means "no limit"
   * (the Docker daemon's default applies). String fields use Docker's
   * size-suffix grammar (`512m`, `1g`); the backend validates.
   */
  limits_memory?: string | null
  limits_cpus?: string | null
  limits_memory_swap?: string | null
  limits_memory_swappiness?: number | null
  limits_memory_reservation?: string | null
  limits_cpuset?: string | null
  limits_cpu_shares?: number | null
  /**
   * "unless-stopped" by default. The backend rejects unknown values.
   */
  restart_policy: RestartPolicy
  /**
   * When set, GitHub App pushes to this branch trigger an automatic
   * deploy. Null/empty disables the listener — the operator can still
   * deploy manually from any branch via the Deploy button.
   */
  auto_deploy_branch?: string | null
  /**
   * Whether the engine injects the standard build-args (PREXEL_APP_ID,
   * PREXEL_DEPLOYMENT_ID, …) into `docker build`. Toggle off when a
   * Dockerfile defines unrelated ARGs of the same name and the
   * collision would either error or invalidate cache.
   */
  build_args_inject: boolean
  /**
   * When true, the engine adds SOURCE_COMMIT=<sha> as a build-arg,
   * which deliberately busts Docker's layer cache between deploys.
   * Useful for buildpack-style images that bake the commit into the
   * runtime; off by default because most apps don't want that.
   */
  build_args_source_commit: boolean
  /**
   * Extra Docker labels stamped on the app container. Keys follow
   * Docker's label grammar (`[a-zA-Z0-9][a-zA-Z0-9._-]*`). Always
   * present (the backend marshals `{}` rather than null) so the UI
   * doesn't have to null-check.
   */
  docker_labels: Record<string, string>
  env_vars: Record<string, string>
  status: AppStatus
  last_deployed_at?: number | null
  /**
   * Free-form labels attached to the app. Backed by the global tag
   * dictionary at `GET /tags`; this field carries just the tag names
   * so the list view can filter without a second round-trip.
   */
  tags: string[]
  created_at: number
  updated_at: number
}

/**
 * Global tag dictionary entry. The list endpoint always returns full
 * rows (with `id` + `created_at`); `color` is optional — older tags
 * created before the color column landed may be null.
 */
export interface Tag {
  id: string
  name: string
  color?: string | null
  created_at: number
}

export type GitSourceType = 'github_app' | 'ssh_key' | 'token'

export interface GitSource {
  id: string
  type: GitSourceType
  name: string
  installation_id?: string | null
  public_key?: string | null
  has_token: boolean
  has_private_key: boolean
  // GitHub App identity (populated for type='github_app').
  app_id?: string
  app_slug?: string
  account_login?: string
  account_type?: 'User' | 'Organization' | string
  /**
   * True when this is a github_app row whose manifest was completed
   * but the operator hasn't selected an account to install it on
   * yet. UI surfaces a "Finalizar instalação" CTA in this state.
   */
  pending_install?: boolean
  created_at: string
}

export interface GitRepository {
  id: number
  name: string
  full_name: string
  private: boolean
  html_url: string
  clone_url: string
  default_branch: string
  language?: string | null
  updated_at?: string | null
}

export interface GitBranch {
  name: string
  sha: string
}

export interface RepositoryInspect {
  repo_url: string
  branch: string
  dockerfile_path?: string | null
  compose_file?: string | null
  build_context: string
  suggested_type: 'dockerfile' | 'docker_compose'
  found_files: string[]
}

export type SSLStatus = 'none' | 'pending' | 'issuing' | 'active' | 'failed' | 'expired'

export interface Domain {
  id: string
  name: string
  app_id?: string | null
  is_primary: boolean
  ssl_status: SSLStatus
  ssl_expires_at?: number | null
  dns_verified: boolean
  dns_verified_at?: number | null
  dns_last_check?: number | null
  dns_check_count: number
  zone_id?: string | null
  covered_by_wildcard: boolean
  /**
   * Per-service routing (Compose apps). When set, Caddy proxies to
   * `prexel-<app>-<service>:<port>` instead of the app's default
   * container. Both set together or both nil — backend enforces.
   */
  service?: string | null
  port?: number | null
  /**
   * When true (default), HTTP requests are 308-redirected to HTTPS by
   * Caddy. Set to false to allow serving plain HTTP alongside HTTPS —
   * useful for external health checks or legacy clients that can't
   * follow redirects.
   */
  force_https: boolean
  created_at: number
  updated_at: number
}

export type DeploymentStatus =
  | 'pending'
  | 'building'
  | 'deploying'
  | 'success'
  | 'failed'
  | 'cancelled'

/**
 * Live container stats snapshot. Backend (handler.Stats) gathers
 * two Docker stats samples ~1s apart to derive CPU% — single-call
 * latency reflects that. UI polls every 5s.
 *
 * `memory_limit_bytes === 0` means "no limit set" (container can use
 * up to the host's memory). The UI should render that as "—" instead
 * of "0%".
 */
export interface AppStats {
  cpu_percent: number
  memory_used_bytes: number
  memory_limit_bytes: number
  /** RFC3339 timestamp of when the sample finished. */
  sampled_at: string
}

/**
 * One row in the AppDetail "Containers" tab. Backend merges live
 * containers (Docker label `prexel.app_id`) with the Compose YAML
 * preview, so a row may be:
 *   - both: full state from Docker + declared ports from YAML
 *   - preview-only: YAML has the service, but it's not running yet
 *     (pre-deploy or stopped). `state === 'preview'`.
 *   - live-only: container running but no YAML to merge into.
 */
export interface AppContainer {
  /** Service name (Compose) or app name (single-container). */
  service: string
  /** Image reference, may be empty for built-locally Compose services. */
  image?: string
  /** "running" / "exited" / "restarting" / ... / "preview". */
  state?: string
  /** Real container name on the Docker host. Empty for preview rows. */
  container_name?: string
  /** Ports the container declares (compose) or app.port (single). */
  ports: number[]
  /** Whether the YAML defines a healthcheck for this service. */
  has_healthcheck: boolean
}

/**
 * Rich projection of one container — used by the dedicated container
 * detail page (/apps/:id/containers/:name). Backend folds docker's
 * ContainerInspect into this trimmed shape so the UI renders without
 * having to read raw Docker JSON.
 *
 * When `preview` is true the row was synthesised from the Compose
 * YAML and there's no live container yet — the SPA hides runtime-
 * only sections (logs, terminal, stats) in that case.
 */
export interface ContainerMount {
  /** "bind" / "volume" / "tmpfs". */
  type: string
  /** Host path (bind) or volume name (volume). */
  source: string
  /** In-container path. */
  destination: string
  read_only: boolean
}

export interface ContainerDetail {
  service?: string
  container_name: string
  image: string
  state: string
  /** Human-readable "Up 2m" / "Exited (1) 5s ago" — built server-side
      from the timestamps for parity with `docker ps`. */
  status: string
  exit_code: number
  ports: number[]
  env: Record<string, string>
  mounts: ContainerMount[]
  labels: Record<string, string>
  cmd: string[] | null
  entrypoint: string[] | null
  networks: string[]
  ip_address?: string
  restart_policy: string
  /** Unix seconds. 0 = unset / never-started. */
  created_at: number
  started_at: number
  finished_at: number
  /** True when the row is synthesised from the Compose YAML (no live
      container yet). UI hides runtime-only sections. */
  preview: boolean
}

export interface Deployment {
  id: string
  app_id: string
  commit_sha?: string | null
  commit_msg?: string | null
  branch?: string | null
  image_tag?: string | null
  rollback_of?: string | null
  status: DeploymentStatus
  log_path?: string | null
  /**
   * Wrapped error chain captured server-side when the deploy failed.
   * Always null for in-flight or successful deploys. Renders verbatim
   * in the deployment-detail error banner — no parsing.
   */
  error_message?: string | null
  started_at?: number | null
  finished_at?: number | null
  created_at: number
}

export interface SecretMeta {
  key: string
  is_build_time: boolean
  is_multiline: boolean
  updated_at: number
}

/**
 * Per-app persistent volume. Backend table `app_volumes`. Two flavours:
 *   - named  (is_named=true):  Docker-managed volume; backend derives a
 *     deterministic host-side name like `prexel-vol-<app>-<hash>`.
 *     `host_path` is null/empty in this mode.
 *   - bind   (is_named=false): bind mount from `host_path` on the
 *     server into `mount_path` inside the container. `host_path` is
 *     required here.
 *
 * `service` scopes the volume to one Compose service. Null = ignored
 * for now (eventually "mount on every service" — currently a no-op
 * for single-container apps where there's only one container).
 */
export interface AppVolume {
  id: string
  app_id: string
  service: string | null
  mount_path: string
  host_path: string | null
  is_named: boolean
  read_only: boolean
  created_at: number
  updated_at: number
}

export interface ApiErrorBody {
  error: string
  message?: string
  details?: Record<string, unknown>
}

// ─── 2FA management types ───
export interface TwoFactorSetupInitiateResponse {
  secret: string
  otpauth_url: string
  qr_code_png_b64: string
}

export interface TwoFactorSetupConfirmResponse {
  recovery_codes: string[]
}

export interface TwoFactorStatusResponse {
  enabled: boolean
  confirmed_at?: number | null
  recovery_codes_remaining: number
}
