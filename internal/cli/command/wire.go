package command

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/prexel/prexel/internal/api"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/appvolume"
	"github.com/prexel/prexel/internal/auth"
	"github.com/prexel/prexel/internal/build"
	"github.com/prexel/prexel/internal/caddy"
	"github.com/prexel/prexel/internal/config"
	"github.com/prexel/prexel/internal/crypto"
	"github.com/prexel/prexel/internal/deploy"
	"github.com/prexel/prexel/internal/dnszone"
	"github.com/prexel/prexel/internal/dockersvc"
	"github.com/prexel/prexel/internal/domains"
	"github.com/prexel/prexel/internal/eventbus"
	"github.com/prexel/prexel/internal/gitsrc"
	"github.com/prexel/prexel/internal/apitoken"
	"github.com/prexel/prexel/internal/backup"
	"github.com/prexel/prexel/internal/instance"
	"github.com/prexel/prexel/internal/rbac"
	"github.com/prexel/prexel/internal/secret"
	"github.com/prexel/prexel/internal/server"
	"github.com/prexel/prexel/internal/setup"
)

// appBuild groups everything `runServe` needs after the wire-up step:
//   - APIDeps    — passed straight to api.NewRouter
//   - background loops (status, dnscheck, sslmonitor, 2FA cleanup) need
//     direct handles to the services that drive them. The Deps struct only
//     carries what HTTP handlers need, so we expose loop targets here.
//
// We used to inline all of this inside runServe (≈130 lines of DI before
// the first net.Listen). That made `serve.go` hard to read and impossible
// to reuse from tests. Moving it to `wire.go` keeps `serve.go` focused on
// the runtime concerns (TLS, listeners, shutdown).
type appBuild struct {
	APIDeps        api.Deps
	ChallengeStore *auth.ChallengeStore
	ServerSvc      *server.Service
	DomainSvc      *domains.Service
	ZoneSvc        *dnszone.Service
	PublicIP       domains.PublicIPProvider
	DeployEngine   *deploy.Engine
	InstanceSvc    *instance.Service
	// DataDir is forwarded so the cleanup loop can probe disk usage on
	// the right partition. Not part of api.Deps because handlers never
	// need it.
	DataDir string
}

// zoneAdapter shrinks dnszone.Service down to the surface
// internal/domains.ZoneLinker expects, mapping the package-local Zone
// type. The internal/domains package can't import internal/dnszone
// directly without creating an import cycle (dnszone never imports
// domains, but a future feature might want the link), and we'd rather
// not couple them by type identity. The adapter lives here in wire.go
// — the only place that knows about both packages.
type zoneAdapter struct {
	svc *dnszone.Service
}

func (a zoneAdapter) EnsureForFQDN(ctx context.Context, fqdn string) (*domains.Zone, error) {
	z, err := a.svc.EnsureForFQDN(ctx, fqdn)
	if err != nil {
		return nil, err
	}
	return &domains.Zone{ID: z.ID, Apex: z.Apex, WildcardVerified: z.WildcardVerified}, nil
}

func (a zoneAdapter) Get(ctx context.Context, id string) (*domains.Zone, error) {
	z, err := a.svc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &domains.Zone{ID: z.ID, Apex: z.Apex, WildcardVerified: z.WildcardVerified}, nil
}

// appvolumeAdapter narrows *appvolume.Service to the
// deploy.AppVolumeLister interface, naming the method ListForApp to
// match the deploy engine's expectation while keeping the appvolume
// package's public API (List) idiomatic from the handler side.
type appvolumeAdapter struct {
	svc *appvolume.Service
}

func (a appvolumeAdapter) ListForApp(ctx context.Context, appID string) ([]appvolume.Volume, error) {
	return a.svc.List(ctx, appID)
}

// buildApp constructs every service the API needs from the bare config + db,
// and returns them grouped for the runServe orchestration. The setup service
// is built early because it surfaces the instance_id used in startup logs.
//
// The caller is responsible for setting `apiDeps.WebDist` on the returned
// struct before passing it to api.NewRouter — `fs.FS` lives one import level
// above to keep this file free of frontend concerns.
func buildApp(cfg *config.Config, database *sql.DB, version string) (*appBuild, error) {
	// cipher protects encrypted columns (secrets, ssh private keys, git
	// credentials, 2FA totp seeds). One instance reused everywhere.
	cipher, err := crypto.New(cfg.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("cipher: %w", err)
	}

	setupSvc, err := setup.NewService(database)
	if err != nil {
		return nil, fmt.Errorf("setup service: %w", err)
	}

	// Instance settings (typed singleton). Loaded eagerly so the cache is
	// hot before maintenance middleware and the deploy semaphore look it up.
	instanceSvc, err := instance.NewService(context.Background(), database)
	if err != nil {
		return nil, fmt.Errorf("instance service: %w", err)
	}

	// 2FA. Challenge store keeps pending TOTP challenges in memory (~5min
	// TTL with a cleanup goroutine kicked off in runServe). PREXEL_2FA_ISSUER
	// is the label that shows up in Google Authenticator etc.
	challengeStore := auth.NewChallengeStore()
	twoFactorSvc := auth.NewTwoFactorService(database, cipher, challengeStore, cfg.TwoFactorIssuer)

	rbacSvc := rbac.NewService(database)
	if err := rbacSvc.EnsureDefaults(context.Background()); err != nil {
		return nil, fmt.Errorf("rbac defaults: %w", err)
	}

	serverSvc := server.NewService(database, cipher)

	// Apps + secrets. app.Service validates server_id (must be 'connected')
	// and optional git_source_id when a repo is wired in. Resource defaults
	// flow from instance settings — newly-created apps without explicit
	// limits inherit them.
	gitSrcRepo := gitsrc.NewRepo(database, cipher)
	appSvc := app.NewService(database, serverSvc, gitSrcRepo).WithDefaults(instanceSvc)
	secretSvc := secret.NewService(database, cipher)

	// Caddy admin lives at http://caddy:2019 (container name on prexel-net).
	// Operators fronting Caddy differently can override with PREXEL_CADDY_ADMIN.
	caddyAdmin := os.Getenv("PREXEL_CADDY_ADMIN")
	if caddyAdmin == "" {
		caddyAdmin = "http://caddy:2019"
	}
	caddyClient := caddy.New(caddyAdmin)

	// Domains carry their own dependency triple — kept separate so a future
	// "headless mode" (no DNS check loop) can swap the publicIP provider.
	publicIP := domains.NewPublicIPProvider("")

	// DNS zones — owns apex tracking + wildcard probes. We construct the
	// service first because domains.NewService needs an adapter pointing
	// at it (auto-create the zone when an FQDN under it is added), and
	// instance.Service uses it to gate instance_url updates against the
	// registered apex list.
	zoneSvc := dnszone.NewService(database, nil)
	instanceSvc.WithZoneRegistry(zoneSvc)

	domainSvc := domains.NewService(domains.Deps{
		DB:       database,
		Caddy:    caddyClient,
		PublicIP: publicIP,
		Zones:    zoneAdapter{svc: zoneSvc},
	})
	// Gate Delete on the panel's current instance_url — we won't let the
	// operator drop a domain row that the running panel is still serving.
	domainSvc.SetInstanceURLReader(instanceSvc)

	// Prime the public IP cache so the first DNS-check pass has a value.
	// Non-fatal — the loop will retry on its own cadence if this fails.
	detectCtx, detectCancel := context.WithTimeout(context.Background(), 6*time.Second)
	if ip, err := publicIP.Get(detectCtx); err != nil {
		slog.Default().Warn("public ip detection failed", "err", err)
	} else if ip != "" {
		slog.Default().Info("public ip detected", "ip", ip)
	}
	detectCancel()

	// Event bus underpins every SSE stream. Buffer holds 1000 events / 30s
	// so a reconnecting EventSource with Last-Event-ID can replay.
	bus := eventbus.New(1000, 30*time.Second)

	// Build engine: clone + tar + docker build / pull. Post-009 the
	// GitHubApp helper is stateless — App credentials are loaded per
	// source by the handlers/cloner, so no env vars are threaded here.
	githubApp := gitsrc.NewGitHubApp()
	cloner := gitsrc.NewCloner(gitSrcRepo, githubApp)
	buildEngine := build.New(cloner, gitSrcRepo, secretSvc, bus)

	// Deploy engine drives the whole zero-downtime swap. Needs its own
	// log dir under DataDir so each deployment can stream a file the
	// Logs endpoint can tail back.
	logDir := filepath.Join(cfg.DataDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	// Per-app volumes (app_volumes rows). The same service is used by
	// the API handler and by the deploy engine — building it once keeps
	// validation behaviour identical across both consumers.
	appVolumeSvc := appvolume.NewService(database, appSvc)
	deployEngine := deploy.New(deploy.Deps{
		DB:         database,
		Apps:       appSvc,
		Servers:    serverSvc,
		Secrets:    secretSvc,
		Domains:    domainSvc,
		Build:      buildEngine,
		Caddy:      caddyClient,
		Bus:        bus,
		LogDir:     logDir,
		Limit:      instanceSvc, // global concurrent-deploys cap from settings
		AppVolumes: appvolumeAdapter{svc: appVolumeSvc},
	})

	// LatestVersion is a process-wide singleton; the checker keeps a 24h
	// in-memory cache so multiple panel sessions reuse the same GitHub
	// answer instead of each one re-querying.
	latestVersionChecker := instance.NewLatestVersionChecker()
	// Personal API tokens (PATs). Stateless service — every method takes
	// ctx and queries directly, so it's safe to share across the API,
	// the auth middleware, and (later) CLI bootstrap helpers.
	apiTokenSvc := apitoken.NewService(database)

	// Backup service. The snapshotter closure binds the secret key
	// and version at wire time (they never change in a single
	// process), and queries instanceSvc for the instance id on
	// every backup so the manifest reflects current truth.
	backupDir := filepath.Join(cfg.DataDir, "backups")
	backupSvc, err := backup.NewService(backupDir, &backupSnapshotter{
		cfg:     cfg,
		setup:   setupSvc,
		version: version,
	})
	if err != nil {
		return nil, fmt.Errorf("backup: init: %w", err)
	}

	apiDeps := api.Deps{
		Cfg:           cfg,
		DB:            database,
		Setup:         setupSvc,
		Servers:       serverSvc,
		Apps:          appSvc,
		Secrets:       secretSvc,
		Domains:       domainSvc,
		DNSZones:      zoneSvc,
		PublicIP:      publicIP,
		Deploy:        deployEngine,
		Bus:           bus,
		Cipher:        cipher,
		TwoFactor:     twoFactorSvc,
		RBAC:          rbacSvc,
		Instance:      instanceSvc,
		LatestVersion: latestVersionChecker,
		APITokens:     apiTokenSvc,
		Backups:       backupSvc,
		Version:       version,
	}

	return &appBuild{
		APIDeps:        apiDeps,
		ChallengeStore: challengeStore,
		ServerSvc:      serverSvc,
		DomainSvc:      domainSvc,
		ZoneSvc:        zoneSvc,
		PublicIP:       publicIP,
		DeployEngine:   deployEngine,
		InstanceSvc:    instanceSvc,
		DataDir:        cfg.DataDir,
	}, nil
}

// startBackgroundLoops kicks off every "periodic worker" the server needs:
//   - servers.Loop          probes every server every 60s, updates DB.
//   - challenge cleanup     sweeps expired 2FA challenges every minute.
//   - domain.Loop           per-domain DNS check every 30s.
//   - domain.SSLLoop        cert state poll every 120s.
//   - dnszone.Loop          apex + wildcard re-verification every 5min.
//   - instance.CleanupLoop  Docker prune + per-app retention sweep (cron-driven).
//
// Returns a stop function that cancels everything; runServe defers it to
// guarantee a clean shutdown.
func startBackgroundLoops(b *appBuild) (stop func()) {
	loopCtx, cancel := context.WithCancel(context.Background())
	go server.Loop(loopCtx, b.ServerSvc, 60*time.Second)
	b.ChallengeStore.StartCleanup(loopCtx, time.Minute)
	go domains.Loop(loopCtx, b.DomainSvc, 30*time.Second)
	go domains.SSLLoop(loopCtx, b.DomainSvc, 120*time.Second)

	// DNS zones: re-probe every 5 minutes. We pass a function rather than a
	// fixed IP so a public-IP change (operator switched ISP / DHCP) is
	// picked up automatically.
	go dnszone.Loop(loopCtx, b.ZoneSvc, 5*time.Minute, func(ctx context.Context) string {
		if b.PublicIP == nil {
			return ""
		}
		ip, _ := b.PublicIP.Get(ctx)
		return ip
	})

	// Cleanup loop targets the LOCAL Docker socket (where Prexel itself runs).
	// Remote servers will get the same treatment in a future fan-out variant.
	// We construct the provider here (not in buildApp) so a misconfigured
	// local socket doesn't block the whole boot — the loop just no-ops if
	// the provider fails to connect.
	localProvider := dockersvc.NewLocalProvider("cleanup")
	target := instance.DockerPruneTarget{Provider: localProvider}
	go instance.CleanupLoop(loopCtx, b.InstanceSvc, target, b.DataDir, nil)

	return cancel
}
