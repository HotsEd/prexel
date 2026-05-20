package command

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/prexel/prexel/internal/api"
	"github.com/prexel/prexel/internal/config"
	"github.com/prexel/prexel/internal/crypto"
	"github.com/prexel/prexel/internal/db"
	"github.com/prexel/prexel/internal/gitsrc"
	prexeltls "github.com/prexel/prexel/internal/tlsutil"
	"github.com/spf13/cobra"
)

func newServeCmd(a Assets) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the Prexel HTTP server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd.Context(), a)
		},
	}
}

// runServe is the runtime orchestration:
//   - load config + open DB + apply migrations
//   - build the service graph (wire.go)
//   - kick off background workers (status / dnscheck / sslmonitor / 2fa cleanup)
//   - resolve TLS cert / key (self-signed fallback)
//   - boot the HTTPS server + an HTTP→HTTPS redirect server
//   - block on a signal, then shut down cleanly
//
// DI used to live inline here — ~130 lines of plumbing made it hard to see
// which calls were actually "runtime concerns". The wire-up moved to
// `wire.go` so this function reads top-to-bottom as "what the binary does
// when you run `prexel serve`".
func runServe(_ context.Context, a Assets) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logger.Info("config loaded", "env", cfg.Env, "port", cfg.Port, "data_dir", cfg.DataDir)

	database, err := db.Open(cfg)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer func() { _ = database.Close() }()

	// Apply migrations on every boot. golang-migrate is idempotent; new
	// versions advance, equal versions noop.
	migrations, err := fs.Sub(a.Migrations, ".")
	if err != nil {
		return fmt.Errorf("migrations sub: %w", err)
	}
	if err := db.Migrate(database, migrations); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	logger.Info("migrations applied")

	// Post-migration backfills that need application-level decryption
	// (can't be done in SQL alone). Idempotent: no-op on fresh DBs
	// and on DBs that have already been upgraded.
	{
		cipher, err := crypto.New(cfg.SecretKey)
		if err != nil {
			return fmt.Errorf("crypto for backfill: %w", err)
		}
		gitRepo := gitsrc.NewRepo(database, cipher)
		if err := gitRepo.BackfillSingletonAppConfig(); err != nil {
			return fmt.Errorf("backfill github_app singleton: %w", err)
		}
	}

	// Build the entire service graph (see wire.go).
	build, err := buildApp(cfg, database, Version)
	if err != nil {
		return err
	}
	// The frontend bundle lives in main.go's embed.FS — we plug it into the
	// API deps right before the router consumes them so wire.go stays free
	// of fs.FS imports.
	build.APIDeps.WebDist = a.WebDist

	// Surface the resolved instance_id immediately so operators see it in logs
	// alongside boot info.
	if instanceID, err := build.APIDeps.Setup.InstanceID(); err == nil {
		logger.Info("instance ready",
			"instance_id", instanceID,
			"setup_completed", build.APIDeps.Setup.Completed(),
		)
	}

	// Public IP detection happens inside buildApp (see wire.go) so the cache
	// is primed before any DNS-check loop starts.

	// Reconcile crash-induced ghost state BEFORE we accept HTTP traffic
	// (Tech Review §14): apps stuck in 'building'/'deploying' from a crash
	// get rolled back to 'error'.
	reconcileCtx, reconcileCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := build.DeployEngine.Reconcile(reconcileCtx); err != nil {
		logger.Warn("reconcile failed", "err", err)
	}
	reconcileCancel()

	router := api.NewRouter(build.APIDeps)

	stopLoops := startBackgroundLoops(build)
	defer stopLoops()

	// Resolve TLS cert/key — generate self-signed under DataDir/tls if absent.
	certPath, keyPath := cfg.TLSCert, cfg.TLSKey
	if certPath == "" || keyPath == "" {
		tlsDir := filepath.Join(cfg.DataDir, "tls")
		certPath = filepath.Join(tlsDir, "server.crt")
		keyPath = filepath.Join(tlsDir, "server.key")
		if err := prexeltls.EnsureSelfSigned(certPath, keyPath); err != nil {
			return fmt.Errorf("tls: %w", err)
		}
	}

	addr := ":" + strconv.Itoa(cfg.Port)
	httpsServer := &http.Server{
		Addr:    addr,
		Handler: router,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		ReadHeaderTimeout: 10 * time.Second,
	}

	// HTTP→HTTPS redirect on :80. The compose file may not expose port 80 in
	// dev; ListenAndServe just logs and continues if the bind fails.
	httpServer := &http.Server{
		Addr:              ":80",
		Handler:           api.HTTPRedirect(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		logger.Info("HTTPS server listening", "addr", addr)
		if err := httpsServer.ListenAndServeTLS(certPath, keyPath); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("https: %w", err)
		}
	}()
	go func() {
		logger.Info("HTTP redirect listening", "addr", ":80")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("http redirect server stopped", "err", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		logger.Info("shutdown signal received", "sig", sig.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpsServer.Shutdown(shutdownCtx)
	_ = httpServer.Shutdown(shutdownCtx)
	return nil
}
