// Package db opens the SQLite database (pure-Go, no CGO) and applies
// migrations using golang-migrate. WAL and foreign keys are enabled.
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/prexel/prexel/internal/config"

	// Register modernc.org/sqlite under the standard database/sql driver name
	// "sqlite" (pure Go — no CGO required).
	_ "modernc.org/sqlite"
)

// Open opens or creates the SQLite database under cfg.DataDir/prexel.db with
// WAL journaling and foreign keys enabled.
func Open(cfg *config.Config) (*sql.DB, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir data dir: %w", err)
	}
	dbPath := filepath.Join(cfg.DataDir, "prexel.db")
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)", dbPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	// modernc.org/sqlite handles concurrency at the SQL layer; we keep a single
	// writer connection to avoid SQLITE_BUSY chains in dev.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	return db, nil
}

// Migrate applies all pending up migrations from migrationsFS, which must be
// rooted at the directory that contains the SQL files (e.g. via fs.Sub).
func Migrate(db *sql.DB, migrationsFS fs.FS) error {
	src, err := iofs.New(migrationsFS, ".")
	if err != nil {
		return fmt.Errorf("iofs: %w", err)
	}
	drv, err := migratesqlite.WithInstance(db, &migratesqlite.Config{})
	if err != nil {
		return fmt.Errorf("migrate driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", drv)
	if err != nil {
		return fmt.Errorf("migrate new: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
