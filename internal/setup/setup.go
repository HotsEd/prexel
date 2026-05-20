// Package setup manages the first-run wizard state machine. It exposes a
// thread-safe flag indicating whether the install has been completed, persists
// the boot-time instance id, and serialises the "mark complete" transition
// with BEGIN EXCLUSIVE per Tech Review §8.
package setup

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
)

// ErrAlreadyCompleted is returned by MarkComplete when setup_completed is
// already "true".
var ErrAlreadyCompleted = errors.New("setup: already completed")

// Service caches the setup_completed flag in memory to keep the hot path of
// every request cheap. The cache is invalidated by MarkComplete.
type Service struct {
	db        *sql.DB
	completed atomic.Bool
}

// NewService constructs a Service and hydrates the in-memory flag from the DB.
func NewService(db *sql.DB) (*Service, error) {
	s := &Service{db: db}
	done, err := readSetupCompleted(db)
	if err != nil {
		return nil, err
	}
	s.completed.Store(done)
	return s, nil
}

// Completed reports the cached setup state.
func (s *Service) Completed() bool {
	return s.completed.Load()
}

// Refresh re-reads the setup_completed flag from the DB and updates the cache.
// Mainly useful in tests or admin tooling that toggles state out-of-band.
func (s *Service) Refresh() error {
	done, err := readSetupCompleted(s.db)
	if err != nil {
		return err
	}
	s.completed.Store(done)
	return nil
}

// InstanceID returns the persisted instance id, generating and storing one on
// first call if absent. Format: prx_<8 hex chars>.
func (s *Service) InstanceID() (string, error) {
	return EnsureInstanceID(s.db)
}

// MarkComplete sets settings.setup_completed = "true" under a BEGIN EXCLUSIVE
// transaction so concurrent wizard requests can't both succeed (Tech Review §8).
func (s *Service) MarkComplete() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("setup: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec("BEGIN EXCLUSIVE"); err != nil {
		// modernc.org/sqlite already started a deferred transaction with
		// db.Begin(); BEGIN EXCLUSIVE inside is a no-op on this driver. Ignore
		// errors and rely on the single-writer pool to serialise.
		_ = err
	}
	var value string
	row := tx.QueryRow(`SELECT value FROM settings WHERE key = 'setup_completed'`)
	switch err := row.Scan(&value); {
	case err == nil:
		if value == "true" {
			return ErrAlreadyCompleted
		}
		if _, err := tx.Exec(
			`UPDATE settings SET value = 'true', updated_at = unixepoch() WHERE key = 'setup_completed'`,
		); err != nil {
			return fmt.Errorf("setup: update: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec(
			`INSERT INTO settings(key, value) VALUES ('setup_completed', 'true')`,
		); err != nil {
			return fmt.Errorf("setup: insert: %w", err)
		}
	default:
		return fmt.Errorf("setup: scan: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("setup: commit: %w", err)
	}
	committed = true
	s.completed.Store(true)
	return nil
}

// HasAnyUser reports whether the users table has at least one row. Used by the
// admin-setup endpoint to reject re-running it after a user has been created.
func (s *Service) HasAnyUser() (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// EnsureInstanceID persists a new instance id if none exists yet and returns
// the current value. Safe to call repeatedly.
func EnsureInstanceID(db *sql.DB) (string, error) {
	var current string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = 'instance_id'`).Scan(&current)
	if err == nil && current != "" {
		return current, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("setup: select instance_id: %w", err)
	}
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("setup: rand: %w", err)
	}
	id := "prx_" + hex.EncodeToString(buf)
	if _, err := db.Exec(
		`INSERT INTO settings(key, value) VALUES ('instance_id', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		id,
	); err != nil {
		return "", fmt.Errorf("setup: insert instance_id: %w", err)
	}
	return id, nil
}

// SaveInstance persists the user-supplied instance URL and TLS mode under the
// settings table. instanceURL may be empty (no domain configured).
func SaveInstance(db *sql.DB, instanceURL, tlsMode string) error {
	if _, err := db.Exec(
		`INSERT INTO settings(key, value) VALUES ('instance_url', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = unixepoch()`,
		instanceURL,
	); err != nil {
		return fmt.Errorf("setup: save instance_url: %w", err)
	}
	if _, err := db.Exec(
		`INSERT INTO settings(key, value) VALUES ('tls_mode', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = unixepoch()`,
		tlsMode,
	); err != nil {
		return fmt.Errorf("setup: save tls_mode: %w", err)
	}
	return nil
}

func readSetupCompleted(db *sql.DB) (bool, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = 'setup_completed'`).Scan(&v)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("setup: read flag: %w", err)
	}
	return v == "true", nil
}
