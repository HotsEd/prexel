// Package api builds the HTTP router and wires middlewares and handlers.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/api/handler"
	"github.com/prexel/prexel/internal/apitoken"
	"github.com/prexel/prexel/internal/backup"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/appcleanup"
	"github.com/prexel/prexel/internal/appvolume"
	"github.com/prexel/prexel/internal/auth"
	"github.com/prexel/prexel/internal/config"
	"github.com/prexel/prexel/internal/crypto"
	"github.com/prexel/prexel/internal/deploy"
	"github.com/prexel/prexel/internal/domains"
	"github.com/prexel/prexel/internal/dnszone"
	"github.com/prexel/prexel/internal/eventbus"
	"github.com/prexel/prexel/internal/gitsrc"
	"github.com/prexel/prexel/internal/instance"
	"github.com/prexel/prexel/internal/rbac"
	"github.com/prexel/prexel/internal/secret"
	"github.com/prexel/prexel/internal/server"
	"github.com/prexel/prexel/internal/setup"
	"github.com/prexel/prexel/internal/tag"
)

// Deps bundles everything the API layer needs.
type Deps struct {
	Cfg       *config.Config
	DB        *sql.DB
	Setup     *setup.Service
	Servers   *server.Service
	Apps      *app.Service
	Secrets   *secret.Service
	Domains   *domains.Service
	Deploy    *deploy.Engine
	Bus       *eventbus.Bus
	Cipher    *crypto.Cipher
	TwoFactor *auth.TwoFactorService
	RBAC      *rbac.Service
	Instance  *instance.Service
	// LatestVersion is the "what's the newest release on GitHub?" checker.
	// Optional — when nil, the latest-version endpoint returns an empty
	// payload (the UI treats that as "I don't know" and hides the badge).
	LatestVersion *instance.LatestVersionChecker
	// APITokens enables Authorization: Bearer prx_pat_… auth alongside
	// JWT. Optional — when nil, only JWTs from /auth/login work (the
	// pre-tokens behaviour, kept for tests + early-boot before the user
	// has any PATs to use).
	APITokens *apitoken.Service
	// Backups optional too — service is only constructed when the
	// snapshotter is wired in cmd.
	Backups  *backup.Service
	DNSZones *dnszone.Service
	// PublicIP lets the DNS-zones handler read the host's public IPv4 when
	// running zone Verify calls. May be nil in tests; the handler will
	// just probe without an expected IP (wildcard still detectable, apex
	// match impossible).
	PublicIP interface {
		Get(ctx context.Context) (string, error)
	}
	Version string
	// WebDist holds the embedded SPA bundle (web/dist). May be nil during tests
	// or when running with no frontend (the router falls back to a tiny landing
	// page in that case).
	WebDist fs.FS
}

// NewRouter builds the Chi router with the full middleware stack and routes.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(apimiddleware.SecurityHeaders)
	r.Use(apimiddleware.CORS(d.Cfg.InstanceURL))
	r.Use(apimiddleware.RequestSize(5 << 20))
	// SetupGate runs after the body cap so wizard 403s never read body.
	r.Use(apimiddleware.SetupGate(d.Setup))
	// Maintenance gate: when the admin flips maintenance_mode on, every API
	// request gets 503 except healthz, /auth/*, and PATCH /instance/settings
	// (which is how the admin disables maintenance back). See the middleware
	// for the full allow-list. Skipped entirely if Instance isn't wired
	// (tests using NewRouter with a partial Deps struct).
	if d.Instance != nil {
		r.Use(apimiddleware.Maintenance(d.Instance))
	}

	// timeout wraps a handler in a 30s context — applied to non-streaming
	// routes only. SSE handlers must NOT be wrapped (they intentionally hold
	// the response writer open).
	timeout := middleware.Timeout(30 * time.Second)

	setupHandler := handler.NewSetupHandler(handler.SetupDeps{
		DB:      d.DB,
		Setup:   d.Setup,
		Servers: d.Servers,
		Version: d.Version,
	})
	serverHandler := handler.NewServerHandler(d.Servers)
	domainHandler := handler.NewDomainHandler(d.Domains)
	// Tags (migration 011) — instance-wide dictionary used to label apps.
	// Built before NewAppHandler so we can wire it into d.Apps and have
	// app responses include the attached tag names. The service has no
	// other dependencies; the per-app endpoints reuse d.Apps + d.RBAC.
	tagSvc := tag.NewService(d.DB)
	d.Apps.WithTags(tagSvc)
	appHandler := handler.NewAppHandler(d.Apps, d.RBAC, d.Servers)

	// Async garbage collector for app deletes. Started here so the
	// worker goroutine outlives any individual HTTP request — the
	// cleanup itself happens out-of-band. The Caddy client is
	// resolved through the deploy engine (which already holds it);
	// when running without the deploy engine wired (rare tests),
	// the cleanup just skips the Caddy step.
	var cleanupCaddy appcleanup.CaddyClient
	var cleanupLogDir string
	if d.Deploy != nil {
		cleanupCaddy = d.Deploy.Caddy
		cleanupLogDir = d.Deploy.LogDir
	}
	cleanupWorker := appcleanup.New(d.Servers, cleanupCaddy, cleanupLogDir, 64)
	cleanupWorker.Start()
	// Adapter: the handler's CleanupSubmitter interface takes a
	// handler.CleanupTask; translate to appcleanup.Task at the
	// boundary so neither package has to import the other.
	appHandler.SetCleanup(cleanupSubmitterAdapter{w: cleanupWorker})
	secretHandler := handler.NewSecretHandler(d.Apps, d.Secrets)
	tagHandler := handler.NewTagHandler(d.Apps, tagSvc, d.RBAC)
	// Per-app persistent volumes (migration 011). Separate service so the
	// deploy engine and the handler share validation; the engine will
	// consume Volume rows directly when wiring container mounts.
	appVolumeSvc := appvolume.NewService(d.DB, d.Apps)
	appVolumeHandler := handler.NewAppVolumeHandler(appVolumeSvc, d.Apps, d.RBAC)
	deploymentHandler := handler.NewDeploymentHandler(d.Apps, d.Deploy)
	eventHandler := handler.NewEventHandler(d.Apps, d.Servers, d.Bus)
	containerLogsHandler := handler.NewContainerLogsHandler(d.Apps, d.Servers, d.RBAC)
	// Container exec (interactive WebSocket terminal). Wired only when
	// we have the bare minimum to authenticate handshakes: the JWT
	// secret is mandatory because browsers can only send the token in
	// the URL (no Authorization header on WebSocket upgrades). PAT
	// support is optional and degrades gracefully if d.APITokens is
	// nil — same shape as the Auth middleware.
	// Pass a typed-nil-safe value: if d.APITokens is nil we must hand
	// the constructor a literal nil so the interface inside the handler
	// compares == nil correctly.
	var execTokens handler.ExecTokenAuthenticator
	if d.APITokens != nil {
		execTokens = d.APITokens
	}
	containerExecHandler := handler.NewContainerExecHandler(
		d.Apps, d.Servers, d.RBAC, d.Cfg.JWTSecret, execTokens,
	).WithDevMode(d.Cfg.IsDevelopment()).WithInstanceURL(d.Cfg.InstanceURL)
	rbacHandler := handler.NewRBACHandler(d.RBAC, d.Cfg.DataDir)
	var instanceHandler *handler.InstanceHandler
	if d.Instance != nil {
		instanceHandler = handler.NewInstanceHandler(d.Instance, d.RBAC, d.LatestVersion)
	}
	// API tokens (PATs). Mounted only when wired — handler panics on nil
	// service, so we keep the construction conditional like Instance does.
	var apiTokenHandler *handler.APITokenHandler
	if d.APITokens != nil {
		apiTokenHandler = handler.NewAPITokenHandler(d.APITokens)
	}
	var backupHandler *handler.BackupHandler
	if d.Backups != nil {
		backupHandler = handler.NewBackupHandler(d.Backups, d.RBAC)
	}
	var dnsZoneHandler *handler.DNSZoneHandler
	if d.DNSZones != nil {
		dnsZoneHandler = handler.NewDNSZoneHandler(d.DNSZones, d.RBAC)
		if d.PublicIP != nil {
			dnsZoneHandler.SetPublicIP(d.PublicIP)
		}
	}
	secureCookies := !d.Cfg.IsDevelopment()
	authHandler := handler.NewAuthHandler(handler.AuthDeps{
		DB:        d.DB,
		Setup:     d.Setup,
		JWTSecret: d.Cfg.JWTSecret,
		Secure:    secureCookies,
		TwoFactor: d.TwoFactor,
		RBAC:      d.RBAC,
	})
	twoFactorHandler := handler.NewTwoFactorHandler(handler.TwoFactorDeps{
		DB:        d.DB,
		Service:   d.TwoFactor,
		JWTSecret: d.Cfg.JWTSecret,
		Secure:    secureCookies,
	})

	// Git sources (A5). Cipher is required to encrypt SSH keys/PATs/App
	// private keys at rest. Post-009 the GitHubApp helper is stateless —
	// it reads App credentials from the *Source passed by handlers, so
	// there are no env vars to thread in here.
	gitRepo := gitsrc.NewRepo(d.DB, d.Cipher)
	githubApp := gitsrc.NewGitHubApp()
	gitSourceDeps := handler.GitSourceDeps{
		Repo: gitRepo,
		App:  githubApp,
	}
	if d.Instance != nil {
		gitSourceDeps.Instance = d.Instance
	}
	gitSourceHandler := handler.NewGitSourceHandler(gitSourceDeps)
	// Webhook receiver — mounted OUTSIDE /api/v1 (and outside every
	// auth middleware) below. GitHub authenticates via HMAC, not JWT;
	// the handler itself does the signature check.
	webhookHandler := handler.NewWebhookHandler(gitRepo, d.Apps, d.Deploy)

	// Wire the compose-YAML fetcher into AppHandler so the Containers
	// endpoint can show a service preview for repo-hosted Compose apps
	// before the first deploy. Resolves the git source per call (cheap
	// DB lookup) and routes by source type:
	//   github_app → API fetch via GitHubApp
	//   anything else → not supported yet (would need a clone, too
	//   costly for a UI render path)
	appHandler.SetComposeFetcher(func(ctx context.Context, target *app.App) ([]byte, error) {
		if target.GitSourceID == nil || target.RepoURL == nil {
			return nil, nil
		}
		src, err := gitRepo.Get(*target.GitSourceID)
		if err != nil {
			return nil, err
		}
		if src.Type != "github_app" {
			return nil, nil
		}
		auth, err := gitRepo.AppAuthFor(src)
		if err != nil {
			return nil, err
		}
		owner, repo, ok := parseGitHubRepoURL(*target.RepoURL)
		if !ok {
			return nil, nil
		}
		composePath := "docker-compose.yml"
		if target.ComposeFile != nil && strings.TrimSpace(*target.ComposeFile) != "" {
			composePath = *target.ComposeFile
		}
		branch := target.Branch
		if strings.TrimSpace(branch) == "" {
			branch = "main"
		}
		return githubApp.GetRepositoryFile(ctx, auth, owner, repo, composePath, branch)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.With(timeout).Get("/healthz", healthz)

		// Setup wizard — public, gated by SetupGate (404 once completed).
		r.Route("/setup", func(r chi.Router) {
			r.Use(timeout)
			r.Get("/status", setupHandler.Status)
			r.Post("/admin", setupHandler.CreateAdmin)
			r.Post("/instance", setupHandler.SaveInstance)
			r.Post("/server", setupHandler.CreateServer)
			r.Post("/complete", setupHandler.Complete)
		})

		// Auth public endpoints. Both /login and /refresh are
		// rate-limited per client IP. The two limiters are
		// independent (separate buckets) so burst behaviour on one
		// can't starve the other. TrustedProxies wires through from
		// the runtime config — see internal/api/apimiddleware/ratelimit.go
		// for the trust model.
		rlCfg := apimiddleware.RateLimitConfig{TrustedProxies: d.Cfg.TrustedProxies}
		r.Group(func(r chi.Router) {
			r.Use(timeout)
			r.Use(apimiddleware.LoginRateLimit(rlCfg))
			r.Post("/auth/login", authHandler.Login)
		})
		r.Group(func(r chi.Router) {
			r.Use(timeout)
			r.Use(apimiddleware.RefreshRateLimit(rlCfg))
			r.Post("/auth/refresh", authHandler.Refresh)
		})

		// Second step of login when 2FA is enabled. Public (no JWT yet) but
		// gated by the in-memory ChallengeStore which only lives 5min and
		// caps attempts at 5.
		if d.TwoFactor != nil {
			r.With(timeout).Post("/auth/2fa/challenge", twoFactorHandler.Challenge)
		}
		if d.RBAC != nil {
			r.With(timeout).Get("/avatars/{name}", rbacHandler.ServeAvatar)
		}

		// Backup download — authenticated but NOT timeout-wrapped.
		// Backups can be tens of MB and slow connections shouldn't
		// have their stream cut at 30s. Same group shape as SSE.
		if backupHandler != nil {
			r.Group(func(r chi.Router) {
				r.Use(apimiddleware.Auth(d.Cfg.JWTSecret, d.APITokens))
				r.Get("/backups/{id}/download", backupHandler.Download)
			})
		}

		// SSE endpoints — authenticated but NOT timeout-wrapped.
		r.Group(func(r chi.Router) {
			r.Use(apimiddleware.Auth(d.Cfg.JWTSecret, d.APITokens))
			r.Get("/events", eventHandler.Global)
			r.Get("/apps/{id}/events", eventHandler.AppEvents)
			r.Get("/apps/{id}/logs", eventHandler.Logs)
			// Per-container logs (Compose / multi-container apps).
			// Same SSE shape as /apps/{id}/logs; the handler validates
			// that the container actually carries the prexel.app_id
			// label for {id} before streaming.
			r.Get("/apps/{id}/containers/{name}/logs", containerLogsHandler.Stream)
		})

		// Authenticated API surface (timeout-wrapped, request-response).
		r.Group(func(r chi.Router) {
			r.Use(timeout)
			r.Use(apimiddleware.Auth(d.Cfg.JWTSecret, d.APITokens))

			r.Post("/auth/logout", authHandler.Logout)
			r.Get("/auth/me", authHandler.Me)
			r.Patch("/auth/profile", authHandler.UpdateProfile)
			r.Post("/auth/email", authHandler.ChangeEmail)
			r.Post("/auth/password", authHandler.ChangePassword)
			if d.RBAC != nil {
				r.Post("/auth/avatar", rbacHandler.UploadAvatar)
				r.Delete("/auth/avatar", rbacHandler.DeleteAvatar)

				r.Get("/permissions", rbacHandler.Permissions)
				r.Get("/roles", rbacHandler.ListRoles)
				r.Post("/roles", rbacHandler.CreateRole)
				r.Patch("/roles/{id}", rbacHandler.UpdateRole)
				r.Delete("/roles/{id}", rbacHandler.DeleteRole)

				r.Get("/members", rbacHandler.ListMembers)
				r.Post("/members", rbacHandler.CreateMember)
				r.Patch("/members/{id}", rbacHandler.UpdateMember)
				r.Delete("/members/{id}", rbacHandler.DeleteMember)

				r.Get("/teams", rbacHandler.ListTeams)
				r.Post("/teams", rbacHandler.CreateTeam)
				r.Get("/teams/{id}", rbacHandler.GetTeam)
				r.Patch("/teams/{id}", rbacHandler.UpdateTeam)
				r.Delete("/teams/{id}", rbacHandler.DeleteTeam)
				r.Put("/teams/{id}/members", rbacHandler.SetTeamMembers)
			}

			// Instance settings (typed singleton). GET is allowed for any
			// authenticated user; PATCH requires settings.instance.manage
			// (enforced inside the handler).
			if instanceHandler != nil {
				r.Get("/instance/settings", instanceHandler.Get)
				r.Patch("/instance/settings", instanceHandler.Patch)
				// Passive update awareness: cached GitHub-releases-latest.
				// Auth-only because it's a settings-page enhancement; the
				// data itself is public.
				r.Get("/instance/latest-version", instanceHandler.LatestVersion)
			}

			// Personal API tokens. Always operate on the calling user;
			// there's no admin path to manage someone else's tokens (a
			// power that's almost never the right answer — operators
			// who really need it can revoke the user instead).
			if apiTokenHandler != nil {
				r.Get("/me/tokens", apiTokenHandler.List)
				r.Post("/me/tokens", apiTokenHandler.Create)
				r.Delete("/me/tokens/{id}", apiTokenHandler.Revoke)
			}

			// Backups. List/Create/Delete go through the timeout-
			// wrapped group; the download endpoint is intentionally
			// outside it (multi-MB file streams, slow client links)
			// and is registered just below.
			if backupHandler != nil {
				r.Get("/backups", backupHandler.List)
				r.Post("/backups", backupHandler.Create)
				r.Delete("/backups/{id}", backupHandler.Delete)
			}

			// DNS zones. List/Get gated by domains.view; mutations by
			// settings.instance.manage (admin work). Verify runs both the
			// apex A-record probe and the wildcard random-subdomain probe.
			if dnsZoneHandler != nil {
				r.Get("/dns-zones", dnsZoneHandler.List)
				r.Post("/dns-zones", dnsZoneHandler.Create)
				r.Get("/dns-zones/{id}", dnsZoneHandler.Get)
				r.Patch("/dns-zones/{id}", dnsZoneHandler.Patch)
				r.Delete("/dns-zones/{id}", dnsZoneHandler.Delete)
				r.Post("/dns-zones/{id}/verify", dnsZoneHandler.Verify)
			}

			// 2FA management surface — pairing wizard, disable, recovery
			// regeneration, status. All authenticated.
			if d.TwoFactor != nil {
				r.Post("/auth/2fa/setup-initiate", twoFactorHandler.InitiateSetup)
				r.Post("/auth/2fa/setup-confirm", twoFactorHandler.ConfirmSetup)
				r.Post("/auth/2fa/disable", twoFactorHandler.Disable)
				r.Post("/auth/2fa/recovery-codes", twoFactorHandler.RegenerateRecoveryCodes)
				r.Get("/auth/2fa/status", twoFactorHandler.Status)
			}

			// Apps (A6). CRUD + status mutation. Deploy/Rollback/Restart/Stop
			// land with A8 via deploymentHandler.
			r.Get("/apps", appHandler.List)
			r.Post("/apps", appHandler.Create)
			r.Get("/apps/{id}", appHandler.Get)
			r.Patch("/apps/{id}", appHandler.Patch)
			r.Delete("/apps/{id}", appHandler.Delete)
			r.Post("/apps/{id}/stop", deploymentHandler.Stop)
			r.Post("/apps/{id}/deploy", deploymentHandler.Deploy)
			r.Post("/apps/{id}/rollback", deploymentHandler.Rollback)
			r.Post("/apps/{id}/restart", deploymentHandler.Restart)
			r.Get("/apps/{id}/deployments", deploymentHandler.ListByApp)
			// Live container stats (CPU%, memory). ~1s of latency per
			// call because Docker's stats stream emits ~1Hz and we
			// need two samples to derive CPU%. UI polls every 5s.
			r.Get("/apps/{id}/stats", appHandler.Stats)
			// Containers (live + Compose YAML preview merged).
			// Used by the AppDetail "Containers" tab to render the
			// per-service table and the assign-domain UI.
			r.Get("/apps/{id}/containers", appHandler.Containers)
			// Per-container detail + stats — backs the dedicated
			// /apps/:id/containers/:name page in the SPA. Detail
			// falls back to a "preview" row when a Compose service
			// has no live container yet (same UX rule as the list
			// endpoint). Stats reads two samples ~1s apart, so the
			// SPA polls at most every few seconds.
			r.Get("/apps/{id}/containers/{name}", appHandler.ContainerDetail)
			r.Get("/apps/{id}/containers/{name}/stats", appHandler.ContainerStats)
			r.Get("/deployments/{id}", deploymentHandler.Get)
			r.Get("/deployments/{id}/logs", deploymentHandler.Logs)

			// Per-app secrets (A6). Values are encrypted at rest and never
			// returned by List. The path param is named `id` (same as the rest
			// of the /apps subtree) so Chi can reuse a single trie node.
			r.Get("/apps/{id}/secrets", secretHandler.List)
			r.Put("/apps/{id}/secrets", secretHandler.Upsert)
			r.Delete("/apps/{id}/secrets/{key}", secretHandler.Delete)

			// Per-app non-secret env vars — stored as JSON inside apps.env_vars.
			r.Put("/apps/{id}/env-vars", appHandler.SetEnvVars)

			// Per-app persistent volumes (migration 011). Named docker
			// volumes OR host bind mounts; the deploy engine reads these
			// rows at container-create time. CRUD only — no separate
			// "deploy" endpoint, mutations land on the next deploy.
			r.Get("/apps/{id}/volumes", appVolumeHandler.List)
			r.Post("/apps/{id}/volumes", appVolumeHandler.Create)
			r.Get("/apps/{id}/volumes/{volID}", appVolumeHandler.Get)
			r.Patch("/apps/{id}/volumes/{volID}", appVolumeHandler.Patch)
			r.Delete("/apps/{id}/volumes/{volID}", appVolumeHandler.Delete)

			// Tags (migration 011). /tags is an instance-wide
			// dictionary — auth-only, no team filter (see package
			// doc on internal/tag). Per-app attach/list are RBAC'd
			// off the containing app's TeamID.
			r.Get("/tags", tagHandler.List)
			r.Post("/tags", tagHandler.Create)
			r.Delete("/tags/{id}", tagHandler.Delete)
			r.Get("/apps/{id}/tags", tagHandler.ListForApp)
			r.Put("/apps/{id}/tags", tagHandler.SetAppTags)

			// Domains (A7). Auth-only; no SSH-key-sized bodies, so this stays
			// on the 1MB group above.
			r.Get("/domains", domainHandler.List)
			r.Post("/domains", domainHandler.Create)
			r.Get("/domains/{id}", domainHandler.Get)
			r.Patch("/domains/{id}", domainHandler.Patch)
			r.Delete("/domains/{id}", domainHandler.Delete)
			r.Post("/domains/{id}/retry", domainHandler.Retry)
			r.Post("/domains/{id}/verify", domainHandler.Verify)
			r.Get("/apps/{app}/domains", domainHandler.ListByApp)

			// Servers (A4). GET/DELETE/test stay on the 1MB body group; POST
			// and PATCH move to the 5MB group below since they accept SSH keys.
			r.Get("/servers", serverHandler.List)
			r.Get("/servers/{id}", serverHandler.Get)
			r.Delete("/servers/{id}", serverHandler.Delete)
			r.Post("/servers/{id}/test", serverHandler.Test)
		})

		// GitHub App OAuth callback — PUBLIC route. GitHub redirects browsers
		// here once the user finishes installing the App, so it cannot require
		// our auth header. The handler renders a self-closing page that posts
		// installation_id back to the SPA via window.opener.postMessage.
		r.Get("/git-sources/github/callback", gitSourceHandler.GitHubCallback)
		r.Get("/git-sources/github/manifest-start", gitSourceHandler.ManifestStart)
		r.Get("/git-sources/github/manifest-callback", gitSourceHandler.ManifestCallback)

		// Endpoints that accept SSH keys allow a larger body (Tech Review §22).
		r.Group(func(r chi.Router) {
			r.Use(timeout)
			r.Use(apimiddleware.Auth(d.Cfg.JWTSecret, d.APITokens))
			r.Use(apimiddleware.RequestSize(5 << 20))

			r.Post("/servers", serverHandler.Create)
			r.Patch("/servers/{id}", serverHandler.Patch)

			// Git sources (A5). CRUD + per-source GitHub App helpers.
			// Post-009: install-url and finalize moved under /{id}
			// because each source carries its own App credentials.
			r.Get("/git-sources", gitSourceHandler.List)
			r.Post("/git-sources", gitSourceHandler.Create)
			r.Post("/git-sources/github/manifest-url", gitSourceHandler.ManifestURL)
			// CLI-driven manifest flow ends here: CLI handled the
			// GitHub OAuth dance locally and ships us the resolved
			// App credentials. See handler.Import for details.
			r.Post("/git-sources/github/import", gitSourceHandler.Import)
			r.Get("/git-sources/{id}", gitSourceHandler.Get)
			r.Delete("/git-sources/{id}", gitSourceHandler.Delete)
			r.Post("/git-sources/{id}/test", gitSourceHandler.Test)
			r.Get("/git-sources/{id}/install-url", gitSourceHandler.InstallURL)
			r.Post("/git-sources/{id}/finalize", gitSourceHandler.Finalize)
			r.Post("/git-sources/{id}/regenerate-webhook", gitSourceHandler.RegenerateWebhook)
			r.Get("/git-sources/{id}/repositories", gitSourceHandler.Repositories)
			r.Post("/git-sources/{id}/repositories/inspect", gitSourceHandler.InspectRepository)
			r.Get("/git-sources/{id}/repositories/{owner}/{repo}/branches", gitSourceHandler.Branches)
		})

		// Container exec (interactive terminal, WebSocket). Lives in
		// its own group at the end on purpose:
		//
		//  - The handler authenticates itself (browsers can't set the
		//    Authorization header on a WebSocket upgrade, so we accept
		//    `?token=…` as a fallback). That means we MUST NOT wrap
		//    this route with apimiddleware.Auth — it would reject the
		//    browser path before our handler can see the query token.
		//  - It must NOT be timeout-wrapped: an interactive shell is
		//    held open for the duration of the user's session, well
		//    past the 30s request-response cap.
		//
		// Path is intentionally identical in shape to
		// /apps/{id}/containers/{name}/logs so callers can swap
		// `/logs` for `/exec` to flip from tailing to interacting.
		r.Get("/apps/{id}/containers/{name}/exec", containerExecHandler.Exec)
	})

	// Mirror /healthz at the root for liveness probes.
	r.With(timeout).Get("/healthz", healthz)

	// GitHub App webhook receiver — PUBLIC. Mounted at the root (outside
	// /api/v1 and outside every auth middleware) because GitHub
	// authenticates via X-Hub-Signature-256 over the request body. The
	// handler verifies the HMAC against the per-source webhook_secret
	// before doing anything else. Wrapped in the standard timeout so a
	// hanging push delivery can't pin a connection forever; the body cap
	// is enforced inside the handler via http.MaxBytesReader.
	if d.Deploy != nil {
		r.With(timeout).Post("/webhooks/github/{source_id}", webhookHandler.Handle)
	}

	// SPA: serve embedded web/dist with history-fallback so client-side routes
	// (/setup, /apps/xxx, etc.) resolve to index.html. The SetupGate middleware
	// still runs first and redirects /setup → /setup for unauthenticated SPA
	// loads pre-setup — but it never blocks asset requests under /assets/.
	if d.WebDist != nil {
		r.Handle("/*", spaHandler(d.WebDist))
	}

	return r
}

// spaHandler serves the embedded SPA, falling back to index.html for unknown
// paths (history API mode in Vue Router).
func spaHandler(distFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(distFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(distFS, path); err != nil {
			// Unknown path → serve index.html so the Vue router can handle it.
			r2 := *r
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, &r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// HTTPRedirect returns a handler that 301s every request to https on the same host.
func HTTPRedirect() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if i := strings.Index(host, ":"); i >= 0 {
			host = host[:i]
		}
		target := "https://" + host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// parseGitHubRepoURL extracts owner + repo from any common GitHub
// URL shape Prexel persists in app.repo_url:
//   https://github.com/owner/repo
//   https://github.com/owner/repo.git
//   git@github.com:owner/repo.git
// Returns ok=false for anything else (non-github, malformed, etc.)
// — callers fall back to "no preview".
func parseGitHubRepoURL(raw string) (owner, repo string, ok bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, ".git")
	// SSH form: git@github.com:owner/repo
	if strings.HasPrefix(s, "git@github.com:") {
		s = strings.TrimPrefix(s, "git@github.com:")
	} else {
		// HTTPS form. Strip scheme + host prefix.
		for _, prefix := range []string{
			"https://github.com/",
			"http://github.com/",
			"github.com/",
		} {
			if strings.HasPrefix(s, prefix) {
				s = strings.TrimPrefix(s, prefix)
				break
			}
		}
	}
	parts := strings.SplitN(s, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// cleanupSubmitterAdapter bridges handler.CleanupSubmitter (the
// AppHandler's narrow interface) with *appcleanup.Worker (the
// concrete implementation). The translation step prevents the
// handler package from importing internal/appcleanup directly,
// keeping the dependency direction clean.
type cleanupSubmitterAdapter struct {
	w *appcleanup.Worker
}

func (a cleanupSubmitterAdapter) Submit(t handler.CleanupTask) error {
	return a.w.Submit(appcleanup.Task{
		AppID:         t.AppID,
		AppName:       t.AppName,
		ServerID:      t.ServerID,
		Domains:       t.Domains,
		DeploymentIDs: t.DeploymentIDs,
	})
}
