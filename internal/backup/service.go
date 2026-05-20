package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

/*
   Backup service — filesystem-only CRUD over the snapshots directory.

   We deliberately keep zero metadata in the SQLite DB: backups are
   stored as discrete files and the listing comes from `fs.ReadDir`
   plus stat. Rationale:

     - Avoids a chicken-and-egg around restore (you'd be reading a
       table whose schema you might be about to replace).
     - One less migration on the critical path.
     - File mtime + name already encode everything the UI needs.

   File names follow `prexel-<RFC3339 timestamp>.prexel-backup`. The
   timestamp is sortable and human-readable; the canonical extension
   lets the UI/CLI recognise the file without sniffing the magic.
*/

// Service is concurrency-safe by virtue of only doing atomic file ops.
type Service struct {
	dir string
	// snapshotter abstracts "give me the current state to back up".
	// Production wiring injects functions that read from instance.Service
	// and the runtime config; tests can swap a fake to avoid spinning
	// up an SQLite.
	snapshotter Snapshotter
}

// Snapshotter is the contract the service needs from its host to
// produce a backup. Kept as an interface (not a struct of values)
// because some of these — notably the secret key — may want to come
// from a fresher source than whatever was loaded at boot.
type Snapshotter interface {
	DBPath() string
	SecretKey() string
	InstanceID(ctx context.Context) (string, error)
	AppVersion() string
}

// Entry is the metadata view used by the API/UI listing. It mirrors
// the manifest fields we can derive purely from a stat (no decrypt).
type Entry struct {
	ID        string    `json:"id"`         // file name without extension
	FileName  string    `json:"file_name"`  // full name with extension
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	ErrInvalidID = errors.New("backup: invalid id")
	ErrNotFound  = errors.New("backup: not found")
)

func NewService(dir string, snap Snapshotter) (*Service, error) {
	if dir == "" {
		return nil, errors.New("backup: directory is required")
	}
	if snap == nil {
		return nil, errors.New("backup: snapshotter is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("backup: mkdir: %w", err)
	}
	return &Service{dir: dir, snapshotter: snap}, nil
}

// Create runs a full snapshot and writes it encrypted to disk.
// Returns the new entry; the caller can hand it to the UI for the
// download link (separate endpoint, never bundled in the create
// response because the user might want to delete and recreate before
// downloading).
func (s *Service) Create(ctx context.Context, passphrase string) (*Entry, error) {
	if len(passphrase) < 12 {
		return nil, ErrWeakPassphrase
	}
	instanceID, err := s.snapshotter.InstanceID(ctx)
	if err != nil {
		return nil, fmt.Errorf("backup: instance id: %w", err)
	}

	now := time.Now().UTC()
	id := "prexel-" + now.Format("2006-01-02T15-04-05Z")
	fileName := id + FileExtension
	path := filepath.Join(s.dir, fileName)

	size, err := Write(Inputs{
		DBPath:     s.snapshotter.DBPath(),
		SecretKey:  s.snapshotter.SecretKey(),
		InstanceID: instanceID,
		AppVersion: s.snapshotter.AppVersion(),
		Passphrase: passphrase,
	}, path)
	if err != nil {
		return nil, err
	}
	return &Entry{
		ID:        id,
		FileName:  fileName,
		SizeBytes: size,
		CreatedAt: now,
	}, nil
}

// List returns every entry in the directory, newest first.
func (s *Service) List() ([]Entry, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Entry{}, nil
		}
		return nil, fmt.Errorf("backup: list: %w", err)
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, FileExtension) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Entry{
			ID:        strings.TrimSuffix(name, FileExtension),
			FileName:  name,
			SizeBytes: info.Size(),
			CreatedAt: info.ModTime().UTC(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// Path returns the absolute path for an entry id, after validating
// the id doesn't try to escape the backup directory. Used by the
// download handler.
func (s *Service) Path(id string) (string, error) {
	name, err := validateID(id)
	if err != nil {
		return "", err
	}
	p := filepath.Join(s.dir, name)
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	return p, nil
}

// Delete removes the backup file.
func (s *Service) Delete(id string) error {
	name, err := validateID(id)
	if err != nil {
		return err
	}
	p := filepath.Join(s.dir, name)
	if err := os.Remove(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return fmt.Errorf("backup: delete: %w", err)
	}
	return nil
}

// validateID guards against `..` injection in the URL parameter. The
// ID must be the bare timestamped stem; anything else is rejected
// before we touch the filesystem.
func validateID(id string) (string, error) {
	if id == "" {
		return "", ErrInvalidID
	}
	// IDs we mint look like "prexel-2026-05-17T12-34-56Z". Restrict
	// the alphabet so injections are obvious. Allow uppercase too in
	// case the human renames a file before re-uploading some day.
	for _, r := range id {
		if !(r == '-' || r == '_' || r == ':' ||
			(r >= '0' && r <= '9') ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z')) {
			return "", ErrInvalidID
		}
	}
	if strings.Contains(id, "..") || strings.HasPrefix(id, ".") {
		return "", ErrInvalidID
	}
	return id + FileExtension, nil
}
