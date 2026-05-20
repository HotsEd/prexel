// Package backup writes/reads passphrase-encrypted instance snapshots.
//
// File layout on disk (extension: `.prexel-backup`):
//
//     ┌──────────────────────────────────────────────────────────────┐
//     │ magic     │ "PRXBKP\x00\x01"    (8 bytes, version 1 marker)  │
//     │ salt      │ 16 bytes (random, used by scrypt)                │
//     │ nonce     │ 12 bytes (random, used by AES-GCM)               │
//     │ ciphertext│ AES-256-GCM(plaintext)  (rest of the file)       │
//     └──────────────────────────────────────────────────────────────┘
//
// Where `plaintext` is a gzipped tarball containing:
//
//     prexel.db          — verbatim copy of the live SQLite database
//     secret_key.txt     — the PREXEL_SECRET_KEY at the moment of backup
//     manifest.json      — instance_id, version, created_at, format_version
//
// Design intent:
//
//   * Self-contained: the secret key travels with the backup, so a
//     restore on a fresh VPS works without the operator needing to also
//     produce the original env vars. The trade-off — backup file is the
//     full master key — is mitigated by passphrase encryption.
//
//   * Passphrase-based: operator picks a passphrase at backup time;
//     scrypt(N=16384, r=8, p=1) stretches it into the AES key. These
//     parameters are intentionally conservative (~16MB / 0.1s on
//     modern CPUs) — enough to make brute-force expensive, light
//     enough that restore on a smaller VPS still completes in a few
//     hundred ms.
//
//   * Format-versioned via the magic bytes. If we ever need to change
//     scrypt params or the algorithm, the magic's last byte bumps and
//     restore can branch on it.
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/scrypt"
)

const (
	// FormatVersion is bumped when the on-disk layout changes in a
	// backward-incompatible way. The magic's last byte mirrors it.
	FormatVersion = 1

	// FileExtension is the canonical suffix; the UI suggests it on
	// download, the CLI rejects anything else on restore.
	FileExtension = ".prexel-backup"

	saltLen  = 16
	nonceLen = 12
	keyLen   = 32

	// scrypt parameters. Chosen for ~0.1s on a typical 2vCPU VPS;
	// well within OWASP's "interactive" guidance band.
	scryptN = 1 << 14 // 16384
	scryptR = 8
	scryptP = 1
)

// magicV1 prefixes every v1 backup file. Helps reject bogus files
// (wrong format, truncated, plain SQLite dumps) before we waste
// time on scrypt.
var magicV1 = []byte{'P', 'R', 'X', 'B', 'K', 'P', 0x00, FormatVersion}

// Manifest is the small JSON sidecar packed inside the tar. Keeping it
// alongside the data avoids the operator having to remember which
// instance a backup came from when they've got several.
type Manifest struct {
	FormatVersion int       `json:"format_version"`
	InstanceID    string    `json:"instance_id"`
	AppVersion    string    `json:"app_version"`
	CreatedAt     time.Time `json:"created_at"`
}

// Errors callers will want to distinguish.
var (
	ErrBadMagic       = errors.New("backup: not a Prexel backup file (bad magic)")
	ErrBadPassphrase  = errors.New("backup: bad passphrase or corrupted file")
	ErrWeakPassphrase = errors.New("backup: passphrase must be at least 12 characters")
	ErrMissingDB      = errors.New("backup: archive missing prexel.db")
)

// Inputs bundles what the writer needs. Kept as a struct (rather than
// 5 positional args) so additions later don't break callers.
type Inputs struct {
	DBPath     string // path to the live SQLite file (we copy, not move)
	SecretKey  string // raw PREXEL_SECRET_KEY value
	InstanceID string // settings.instance_id for the manifest
	AppVersion string // runtime version string for the manifest
	Passphrase string // operator-chosen, ≥ 12 chars
}

// Write packs the inputs into `outPath`, atomically (writes to a temp
// file in the same dir, then renames). Returns the final size in bytes.
func Write(in Inputs, outPath string) (int64, error) {
	if len(in.Passphrase) < 12 {
		return 0, ErrWeakPassphrase
	}

	// Build the plaintext tarball in memory first. Backups are tiny
	// (SQLite + ~64 bytes of metadata), so we don't bother streaming.
	plain, err := buildArchive(in)
	if err != nil {
		return 0, err
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return 0, fmt.Errorf("backup: salt: %w", err)
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return 0, fmt.Errorf("backup: nonce: %w", err)
	}

	key, err := deriveKey(in.Passphrase, salt)
	if err != nil {
		return 0, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return 0, fmt.Errorf("backup: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return 0, fmt.Errorf("backup: gcm: %w", err)
	}
	cipherText := gcm.Seal(nil, nonce, plain, magicV1) // magic as AAD

	// Atomic rename: write to a sibling tmp, fsync, rename. If the
	// process dies mid-write, the final file never exists.
	tmp, err := os.CreateTemp(filepath.Dir(outPath), ".prxbkp-*")
	if err != nil {
		return 0, fmt.Errorf("backup: temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed

	if _, err := tmp.Write(magicV1); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if _, err := tmp.Write(salt); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if _, err := tmp.Write(nonce); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if _, err := tmp.Write(cipherText); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		return 0, fmt.Errorf("backup: rename: %w", err)
	}
	st, err := os.Stat(outPath)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// ReadResult is what a successful Read hands back. The caller is
// expected to consume Plaintext immediately and zero it once done —
// keeping it on the heap longer than necessary makes the
// post-process key recovery for an attacker that much easier.
type ReadResult struct {
	Manifest  Manifest
	DBBytes   []byte // contents of prexel.db
	SecretKey string // PREXEL_SECRET_KEY recovered from the archive
}

// Read decrypts and unpacks the file at `path`. Returns ErrBadMagic if
// the prefix is wrong, ErrBadPassphrase if AEAD authentication fails
// (which conflates "wrong key" with "tampered ciphertext" — that's
// fine, the operator can't act on the distinction).
func Read(path, passphrase string) (*ReadResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	header := make([]byte, len(magicV1)+saltLen+nonceLen)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, ErrBadMagic
	}
	if !bytes.Equal(header[:len(magicV1)], magicV1) {
		return nil, ErrBadMagic
	}
	salt := header[len(magicV1) : len(magicV1)+saltLen]
	nonce := header[len(magicV1)+saltLen:]

	cipherText, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("backup: gcm: %w", err)
	}
	plain, err := gcm.Open(nil, nonce, cipherText, magicV1)
	if err != nil {
		// Any GCM error here is operator-actionable as "the passphrase
		// is wrong, or the file is corrupted". The two cases are
		// indistinguishable cryptographically — that's by design.
		return nil, ErrBadPassphrase
	}
	return unpackArchive(plain)
}

// ── internals ──

func buildArchive(in Inputs) ([]byte, error) {
	dbBytes, err := os.ReadFile(in.DBPath)
	if err != nil {
		return nil, fmt.Errorf("backup: read db: %w", err)
	}

	manifest := Manifest{
		FormatVersion: FormatVersion,
		InstanceID:    in.InstanceID,
		AppVersion:    in.AppVersion,
		CreatedAt:     time.Now().UTC(),
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("backup: manifest: %w", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	if err := addFile(tw, "prexel.db", dbBytes); err != nil {
		return nil, err
	}
	if err := addFile(tw, "secret_key.txt", []byte(in.SecretKey)); err != nil {
		return nil, err
	}
	if err := addFile(tw, "manifest.json", manifestBytes); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func addFile(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o600, // sensitive payload; restrict bits on extract
		Size:    int64(len(data)),
		ModTime: time.Now().UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func unpackArchive(plain []byte) (*ReadResult, error) {
	gz, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		return nil, fmt.Errorf("backup: gunzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	out := &ReadResult{}
	var foundDB bool

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("backup: tar: %w", err)
		}
		// Defence against tar slip — we never write to disk from this
		// routine but a future caller might forward Name verbatim.
		clean := filepath.Clean(hdr.Name)
		if clean != hdr.Name || filepath.IsAbs(clean) {
			return nil, fmt.Errorf("backup: bad entry name %q", hdr.Name)
		}

		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		switch hdr.Name {
		case "prexel.db":
			out.DBBytes = data
			foundDB = true
		case "secret_key.txt":
			out.SecretKey = string(bytes.TrimSpace(data))
		case "manifest.json":
			if err := json.Unmarshal(data, &out.Manifest); err != nil {
				return nil, fmt.Errorf("backup: manifest decode: %w", err)
			}
		default:
			// Unknown entries are ignored. Lets us extend the format
			// (additive) without breaking old restores.
		}
	}
	if !foundDB {
		return nil, ErrMissingDB
	}
	return out, nil
}

func deriveKey(passphrase string, salt []byte) ([]byte, error) {
	k, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, keyLen)
	if err != nil {
		return nil, fmt.Errorf("backup: scrypt: %w", err)
	}
	return k, nil
}
