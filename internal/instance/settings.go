// Package instance owns the typed "global configuration" of the running
// Prexel server — the row in instance_settings (singleton id=1).
//
// Three roles in one package:
//   - Settings: the data shape (matches migration 006 1:1).
//   - Service: read/update with validation + an in-memory atomic cache so
//     hot-path callers (maintenance middleware, deploy semaphore) don't
//     hit the DB on every request.
//   - cleanup loop (cleanup.go) and concurrent-deploys semaphore (used by
//     deploy.Engine) — they read Cached() to react to live setting changes.
//
// We deliberately do not surface a generic key-value get/set API. The schema
// is opinionated; new knobs ship as schema migrations. This keeps the
// frontend and CLI honest about what they're allowed to change.
package instance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

// Settings mirrors the instance_settings row exactly.
type Settings struct {
	InstanceURL            string `json:"instance_url"`
	TLSMode                string `json:"tls_mode"`
	DefaultMemoryLimit     string `json:"default_memory_limit"`
	DefaultCPULimit        string `json:"default_cpu_limit"`
	CleanupEnabled         bool   `json:"cleanup_enabled"`
	CleanupSchedule        string `json:"cleanup_schedule"`
	CleanupDiskThreshold   int    `json:"cleanup_disk_threshold"`
	CleanupImageRetention  int    `json:"cleanup_image_retention"`
	MaxConcurrentDeploys   int    `json:"max_concurrent_deploys"`
	MaintenanceMode        bool   `json:"maintenance_mode"`
	MaintenanceMessage     string `json:"maintenance_message"`
	UpdatedAt              int64  `json:"updated_at"`
}

// TLS modes accepted by the schema.
const (
	TLSModeSelfSigned  = "self-signed"
	TLSModeLetsEncrypt = "letsencrypt"
)

// UpdateInput uses pointers so an absent field means "don't change".
// Zero-value strings stay distinguishable from "set to empty" — empty
// instance_url is meaningful (means "no domain configured yet").
type UpdateInput struct {
	InstanceURL           *string `json:"instance_url"`
	TLSMode               *string `json:"tls_mode"`
	DefaultMemoryLimit    *string `json:"default_memory_limit"`
	DefaultCPULimit       *string `json:"default_cpu_limit"`
	CleanupEnabled        *bool   `json:"cleanup_enabled"`
	CleanupSchedule       *string `json:"cleanup_schedule"`
	CleanupDiskThreshold  *int    `json:"cleanup_disk_threshold"`
	CleanupImageRetention *int    `json:"cleanup_image_retention"`
	MaxConcurrentDeploys  *int    `json:"max_concurrent_deploys"`
	MaintenanceMode       *bool   `json:"maintenance_mode"`
	MaintenanceMessage    *string `json:"maintenance_message"`
}

// Validation errors. ErrInvalidInput carries a message — handlers should
// surface it as the response body so the UI can render context.
var (
	ErrInvalidInput        = errors.New("instance: invalid input")
	ErrLetsEncryptNeedsURL = errors.New("instance: letsencrypt requires instance_url")
	// ErrZoneNotRegistered fires when the operator tries to set an
	// instance_url whose apex isn't in dns_zones. The user explicitly
	// asked that every hostname Caddy will serve be pre-registered as
	// a zone — this is the gate that enforces it for the panel domain.
	ErrZoneNotRegistered = errors.New("instance: DNS zone for this hostname is not registered")
)

// Service is the read/write surface.
// ZoneRegistry is the smallest surface internal/instance needs from
// internal/dnszone to enforce "the instance URL must point at a zone the
// operator already registered". Defined here as a tiny interface so the
// instance package doesn't import dnszone directly (the production
// adapter lives in wire.go).
type ZoneRegistry interface {
	// ApexRegistered reports whether `apex` is present in dns_zones.
	// Implementations must be fast — this runs on every Update call.
	ApexRegistered(ctx context.Context, apex string) (bool, error)
	// ApexOf returns the registrable apex of fqdn (same rules as
	// dnszone.ApexOf). Lets callers reject FQDNs whose apex can't be
	// determined (internal names, IP literals) before we even look in
	// the DB.
	ApexOf(fqdn string) (string, error)
}

type Service struct {
	db    *sql.DB
	cache atomic.Pointer[Settings]
	zones ZoneRegistry
}

// NewService reads the singleton row into the cache once and returns. Callers
// must not bypass the service to read instance_settings; the cache is the
// source of truth between updates.
func NewService(ctx context.Context, db *sql.DB) (*Service, error) {
	s := &Service{db: db}
	if _, err := s.reload(ctx); err != nil {
		return nil, fmt.Errorf("instance: initial load: %w", err)
	}
	return s, nil
}

// WithZoneRegistry attaches the zone registry used to gate instance_url
// changes. Chainable so wire.go can construct the service before dnszone
// is built. When the registry is nil, instance_url is accepted without
// the zone check (matches the pre-feature behaviour — useful for tests).
func (s *Service) WithZoneRegistry(z ZoneRegistry) *Service {
	s.zones = z
	return s
}

// Get returns a snapshot from the DB (forces a refresh of the cache).
// Use Cached() on hot paths.
func (s *Service) Get(ctx context.Context) (*Settings, error) {
	return s.reload(ctx)
}

// Cached returns the last-known snapshot. Always safe to call (it returns
// a sane zero-valued snapshot if NewService wasn't run first, which only
// happens in tests).
func (s *Service) Cached() Settings {
	if p := s.cache.Load(); p != nil {
		return *p
	}
	return Settings{TLSMode: TLSModeSelfSigned, MaxConcurrentDeploys: 3, CleanupImageRetention: 5}
}

// Update applies the patch atomically and refreshes the cache.
func (s *Service) Update(ctx context.Context, in UpdateInput) (*Settings, error) {
	current, err := s.reload(ctx)
	if err != nil {
		return nil, err
	}

	next := *current
	if in.InstanceURL != nil {
		v := normaliseInstanceURL(*in.InstanceURL)
		// Gate: any non-empty instance_url must point at a registered
		// dns_zone (the user's rule: "every domain Caddy serves has to
		// be cadastrado"). Empty stays valid — that's the "no domain
		// configured yet, use IP" case.
		if v != "" && s.zones != nil {
			apex, err := s.zones.ApexOf(v)
			if err != nil {
				return nil, fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())
			}
			registered, err := s.zones.ApexRegistered(ctx, apex)
			if err != nil {
				return nil, fmt.Errorf("instance: zone lookup: %w", err)
			}
			if !registered {
				return nil, fmt.Errorf("%w: register zone %q first", ErrZoneNotRegistered, apex)
			}
		}
		next.InstanceURL = v
	}
	if in.TLSMode != nil {
		v := strings.TrimSpace(*in.TLSMode)
		if v != TLSModeSelfSigned && v != TLSModeLetsEncrypt {
			return nil, fmt.Errorf("%w: tls_mode must be self-signed or letsencrypt", ErrInvalidInput)
		}
		next.TLSMode = v
	}
	if in.DefaultMemoryLimit != nil {
		v := strings.TrimSpace(*in.DefaultMemoryLimit)
		if v != "" && !memRegex.MatchString(v) {
			return nil, fmt.Errorf("%w: default_memory_limit must look like '512m' or '1g'", ErrInvalidInput)
		}
		next.DefaultMemoryLimit = v
	}
	if in.DefaultCPULimit != nil {
		v := strings.TrimSpace(*in.DefaultCPULimit)
		if v != "" && !cpuRegex.MatchString(v) {
			return nil, fmt.Errorf("%w: default_cpu_limit must look like '0.5' or '1.0'", ErrInvalidInput)
		}
		next.DefaultCPULimit = v
	}
	if in.CleanupEnabled != nil {
		next.CleanupEnabled = *in.CleanupEnabled
	}
	if in.CleanupSchedule != nil {
		v := strings.TrimSpace(*in.CleanupSchedule)
		if v != "" && !cronLikeRegex.MatchString(v) {
			return nil, fmt.Errorf("%w: cleanup_schedule must be a 5-field cron expression", ErrInvalidInput)
		}
		next.CleanupSchedule = v
	}
	if in.CleanupDiskThreshold != nil {
		if *in.CleanupDiskThreshold < 0 || *in.CleanupDiskThreshold > 100 {
			return nil, fmt.Errorf("%w: cleanup_disk_threshold must be 0..100", ErrInvalidInput)
		}
		next.CleanupDiskThreshold = *in.CleanupDiskThreshold
	}
	if in.CleanupImageRetention != nil {
		if *in.CleanupImageRetention < 1 {
			return nil, fmt.Errorf("%w: cleanup_image_retention must be >= 1", ErrInvalidInput)
		}
		next.CleanupImageRetention = *in.CleanupImageRetention
	}
	if in.MaxConcurrentDeploys != nil {
		if *in.MaxConcurrentDeploys < 1 {
			return nil, fmt.Errorf("%w: max_concurrent_deploys must be >= 1", ErrInvalidInput)
		}
		next.MaxConcurrentDeploys = *in.MaxConcurrentDeploys
	}
	if in.MaintenanceMode != nil {
		next.MaintenanceMode = *in.MaintenanceMode
	}
	if in.MaintenanceMessage != nil {
		next.MaintenanceMessage = strings.TrimSpace(*in.MaintenanceMessage)
	}

	// Cross-field invariants — checked after merging so we evaluate against
	// the post-patch state.
	if next.TLSMode == TLSModeLetsEncrypt && next.InstanceURL == "" {
		return nil, ErrLetsEncryptNeedsURL
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE instance_settings SET
			instance_url            = ?,
			tls_mode                = ?,
			default_memory_limit    = NULLIF(?, ''),
			default_cpu_limit       = NULLIF(?, ''),
			cleanup_enabled         = ?,
			cleanup_schedule        = ?,
			cleanup_disk_threshold  = ?,
			cleanup_image_retention = ?,
			max_concurrent_deploys  = ?,
			maintenance_mode        = ?,
			maintenance_message     = ?
		WHERE id = 1
	`,
		next.InstanceURL, next.TLSMode,
		next.DefaultMemoryLimit, next.DefaultCPULimit,
		boolToInt(next.CleanupEnabled), next.CleanupSchedule,
		next.CleanupDiskThreshold, next.CleanupImageRetention,
		next.MaxConcurrentDeploys,
		boolToInt(next.MaintenanceMode), next.MaintenanceMessage,
	); err != nil {
		return nil, fmt.Errorf("instance: update: %w", err)
	}

	return s.reload(ctx)
}

// reload reads from the DB, stores in the cache, returns the snapshot.
func (s *Service) reload(ctx context.Context) (*Settings, error) {
	out := Settings{}
	var (
		memNull, cpuNull             sql.NullString
		cleanupEnabled, maintenance  int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT instance_url, tls_mode,
		       default_memory_limit, default_cpu_limit,
		       cleanup_enabled, cleanup_schedule,
		       cleanup_disk_threshold, cleanup_image_retention,
		       max_concurrent_deploys,
		       maintenance_mode, maintenance_message,
		       updated_at
		FROM instance_settings WHERE id = 1
	`).Scan(
		&out.InstanceURL, &out.TLSMode,
		&memNull, &cpuNull,
		&cleanupEnabled, &out.CleanupSchedule,
		&out.CleanupDiskThreshold, &out.CleanupImageRetention,
		&out.MaxConcurrentDeploys,
		&maintenance, &out.MaintenanceMessage,
		&out.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if memNull.Valid {
		out.DefaultMemoryLimit = memNull.String
	}
	if cpuNull.Valid {
		out.DefaultCPULimit = cpuNull.String
	}
	out.CleanupEnabled = cleanupEnabled == 1
	out.MaintenanceMode = maintenance == 1
	s.cache.Store(&out)
	return &out, nil
}

// CurrentInstanceURL satisfies internal/domains.InstanceURLReader. Returns
// the cached panel URL — empty string when the panel runs in IP/self-signed
// mode. The domains package uses this to refuse deleting a domain row that
// the panel itself is currently serving on.
func (s *Service) CurrentInstanceURL() string {
	return s.Cached().InstanceURL
}

// DefaultLimits implements app.ResourceDefaults. Returns the configured
// (memory, cpu) defaults — empty strings mean "no default", which the app
// package interprets as "no limit".
func (s *Service) DefaultLimits() (memory, cpu string) {
	c := s.Cached()
	return c.DefaultMemoryLimit, c.DefaultCPULimit
}

// MaxConcurrentDeploys exposes the cap for the deploy engine's global
// semaphore. Always returns a positive integer.
func (s *Service) MaxConcurrentDeploys() int {
	v := s.Cached().MaxConcurrentDeploys
	if v < 1 {
		return 1
	}
	return v
}

// CleanupConfig is the snapshot the cleanup loop reads each tick.
type CleanupConfig struct {
	Enabled         bool
	Schedule        string
	DiskThreshold   int
	ImageRetention  int
}

// Cleanup returns the cleanup knobs from the cache.
func (s *Service) Cleanup() CleanupConfig {
	c := s.Cached()
	return CleanupConfig{
		Enabled:        c.CleanupEnabled,
		Schedule:       c.CleanupSchedule,
		DiskThreshold:  c.CleanupDiskThreshold,
		ImageRetention: c.CleanupImageRetention,
	}
}

// --- helpers ------------------------------------------------------------

var (
	// Re-using the regexes the app package already validates against so
	// "default" and "per-app" inputs accept exactly the same syntax.
	memRegex      = regexp.MustCompile(`^\d+(?:\.\d+)?[mgkbMGKB]?$`)
	cpuRegex      = regexp.MustCompile(`^\d+(?:\.\d+)?$`)
	// cron-ish: 5 whitespace-separated fields. Real cron parsing happens in
	// the cleanup loop (robfig/cron); this is a fast input sanity check.
	cronLikeRegex = regexp.MustCompile(`^\s*\S+\s+\S+\s+\S+\s+\S+\s+\S+\s*$`)
)

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// normaliseInstanceURL strips http(s):// and trailing slash so the value the
// admin types lands in the DB the same shape the setup wizard already saves.
// Empty string is preserved (= "no domain yet, use IP").
func normaliseInstanceURL(raw string) string {
	v := strings.TrimSpace(raw)
	v = strings.TrimPrefix(v, "https://")
	v = strings.TrimPrefix(v, "http://")
	v = strings.TrimPrefix(v, "www.")
	v = strings.TrimRight(v, "/")
	return v
}
