package command

import (
	"context"
	"path/filepath"

	"github.com/prexel/prexel/internal/config"
	"github.com/prexel/prexel/internal/setup"
)

/*
   backupSnapshotter glues the backup package to the runtime values it
   needs to take a snapshot. We keep this in the cli/command package
   (rather than internal/backup) because it depends on config + setup
   — packages the backup core stays oblivious to so it's testable in
   isolation.

   `cfg.SecretKey` is captured at wire time and reused. That's fine
   because changing PREXEL_SECRET_KEY requires a process restart
   anyway (it's an env var) — the snapshotter can't observe a stale
   value mid-run. The instance id is fetched per-call via setup.Service
   (lives in the `settings` k/v row, not in instance.Settings).
*/
type backupSnapshotter struct {
	cfg     *config.Config
	setup   *setup.Service
	version string
}

func (s *backupSnapshotter) DBPath() string {
	return filepath.Join(s.cfg.DataDir, "prexel.db")
}

func (s *backupSnapshotter) SecretKey() string {
	return s.cfg.SecretKey
}

func (s *backupSnapshotter) InstanceID(ctx context.Context) (string, error) {
	_ = ctx // setup.InstanceID is sync; signature stays ctx-aware for symmetry.
	return s.setup.InstanceID()
}

func (s *backupSnapshotter) AppVersion() string {
	return s.version
}
