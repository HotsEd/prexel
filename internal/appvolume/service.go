package appvolume

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/app"
)

// Validation regexes.
var (
	// serviceRegex mirrors the docker-compose service-name rules: must
	// start with [A-Za-z0-9], followed by alphanumerics, dot, dash, or
	// underscore. We intentionally don't try to verify the service
	// actually exists in the compose YAML — the YAML can change between
	// the time the volume is declared and the next deploy, and the
	// deploy engine is the right place to deal with mismatches.
	serviceRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
)

const (
	maxPathLen = 512
)

// Service is the domain entry point for the app_volumes table. It depends
// on app.Service for FK validation — every volume must point at a real
// app, and we'd rather catch that here than wait for a SQLite FK error to
// bubble up as an opaque "constraint failed".
type Service struct {
	repo *repo
	apps *app.Service
}

// NewService wires a Service. Both arguments are required; passing nil
// would only show up as a runtime nil-deref later, so we keep the
// constructor permissive (matches the rest of the codebase) but expect
// callers to honour the contract.
func NewService(db *sql.DB, appSvc *app.Service) *Service {
	return &Service{repo: newRepo(db), apps: appSvc}
}

// CreateInput is the union of fields accepted by Create.
type CreateInput struct {
	AppID     string
	Service   *string
	MountPath string
	HostPath  *string
	IsNamed   bool
	ReadOnly  bool
}

// UpdatePatch is the partial-update payload for Update. AppID and Service
// are intentionally absent — moving a volume between apps or services
// would invalidate the on-disk data the operator already wrote into it.
// If the operator really needs that, they should delete and recreate.
type UpdatePatch struct {
	MountPath *string
	HostPath  *string
	IsNamed   *bool
	ReadOnly  *bool
}

// Create validates the input, verifies the app exists, and persists the row.
// Returns ErrDuplicate when the (app_id, service, mount_path) tuple already
// exists — the API layer maps that to a 409.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Volume, error) {
	appID := strings.TrimSpace(in.AppID)
	if appID == "" {
		return nil, errors.New("invalid_app_id: required")
	}
	// FK preflight. The deploy engine and the API both ultimately
	// dereference the app, so a missing FK at this layer would just
	// defer the failure to a less helpful spot.
	a, err := s.apps.Get(ctx, appID)
	if err != nil {
		if errors.Is(err, app.ErrNotFound) {
			return nil, fmt.Errorf("invalid_app_id: %w", err)
		}
		return nil, err
	}
	svc, err := normaliseService(in.Service)
	if err != nil {
		return nil, err
	}
	mount, err := validateMountPath(in.MountPath)
	if err != nil {
		return nil, err
	}
	host, err := validateHostPath(in.HostPath)
	if err != nil {
		return nil, err
	}
	if err := validatePairing(in.IsNamed, host); err != nil {
		return nil, err
	}

	// Application-level uniqueness for the (app_id, NULL, mount_path) case:
	// SQLite treats NULL as distinct in UNIQUE constraints, so the schema's
	// UNIQUE(app_id, service, mount_path) only catches collisions when
	// `service` is non-NULL. Single-container apps would otherwise be able
	// to declare two mounts at the same in-container path, which the deploy
	// engine has no sensible answer for. We surface the same ErrDuplicate
	// either way so callers don't have to special-case it.
	if svc == nil {
		dup, err := s.findExisting(ctx, a.ID, mount)
		if err != nil {
			return nil, err
		}
		if dup {
			return nil, ErrDuplicate
		}
	}

	v := &Volume{
		ID:        uuid.NewString(),
		AppID:     a.ID,
		Service:   svc,
		MountPath: mount,
		HostPath:  host,
		IsNamed:   in.IsNamed,
		ReadOnly:  in.ReadOnly,
	}
	if err := s.repo.insert(ctx, v); err != nil {
		if errors.Is(err, ErrDuplicate) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return s.repo.get(ctx, v.ID)
}

// findExisting reports whether an `(app_id, NULL service, mount_path)` row
// already exists. Only used by Create to backfill the gap left by SQLite's
// NULL-distinct UNIQUE semantics — see the comment at the call site.
func (s *Service) findExisting(ctx context.Context, appID, mount string) (bool, error) {
	rows, err := s.repo.listByApp(ctx, appID)
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if r.Service == nil && r.MountPath == mount {
			return true, nil
		}
	}
	return false, nil
}

// List returns every volume of an app. Returns an empty slice (not nil)
// when there are no rows so the JSON encoding stays `[]` instead of `null`.
func (s *Service) List(ctx context.Context, appID string) ([]Volume, error) {
	out, err := s.repo.listByApp(ctx, strings.TrimSpace(appID))
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Volume{}
	}
	return out, nil
}

// Get returns a single volume by id.
func (s *Service) Get(ctx context.Context, id string) (*Volume, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrNotFound
	}
	return s.repo.get(ctx, id)
}

// Update applies a partial mutation. We re-validate the final state of
// (is_named, host_path) after applying the patch, otherwise an operator
// could flip is_named=false without supplying a host_path and end up with
// a row the deploy engine can't act on.
func (s *Service) Update(ctx context.Context, id string, p UpdatePatch) (*Volume, error) {
	cur, err := s.repo.get(ctx, id)
	if err != nil {
		return nil, err
	}

	sets := map[string]any{}

	// Compute the post-patch values so we can validate them as a unit.
	finalIsNamed := cur.IsNamed
	if p.IsNamed != nil {
		finalIsNamed = *p.IsNamed
		sets["is_named"] = boolToInt(*p.IsNamed)
	}
	finalHost := cur.HostPath
	if p.HostPath != nil {
		host, err := validateHostPath(p.HostPath)
		if err != nil {
			return nil, err
		}
		finalHost = host
		sets["host_path"] = nullableStr(host)
	}
	if err := validatePairing(finalIsNamed, finalHost); err != nil {
		return nil, err
	}

	if p.MountPath != nil {
		mount, err := validateMountPath(*p.MountPath)
		if err != nil {
			return nil, err
		}
		sets["mount_path"] = mount
	}
	if p.ReadOnly != nil {
		sets["read_only"] = boolToInt(*p.ReadOnly)
	}

	out, err := s.repo.updateFields(ctx, id, sets)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes the volume row by id. The on-disk data (named volume
// contents or bind-mount directory) is NOT touched — that's an
// intentional safety net so a config mistake doesn't wipe the user's
// data. Cleanup of orphaned named volumes is a manual `docker volume rm`.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.delete(ctx, strings.TrimSpace(id))
}

// ---------------------------------------------------------------------------
// validation helpers
// ---------------------------------------------------------------------------

// validateMountPath enforces the in-container path rules. We require an
// absolute path because docker interprets relative mount paths as
// volume-name references, which is almost never what the operator means.
// `..` is rejected to keep the path canonical — an operator setting
// `/var/data/../etc` would still be valid docker but it's a red flag we'd
// rather block than silently accept.
func validateMountPath(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", errors.New("invalid_mount_path: required")
	}
	if len(v) > maxPathLen {
		return "", fmt.Errorf("invalid_mount_path: max %d chars", maxPathLen)
	}
	if !strings.HasPrefix(v, "/") {
		return "", errors.New("invalid_mount_path: must be absolute (start with /)")
	}
	if containsParentSegment(v) {
		return "", errors.New("invalid_mount_path: must not contain '..' segments")
	}
	return v, nil
}

// validateHostPath applies the same rules as validateMountPath but on the
// host-side path of a bind mount. Returns nil (not an empty pointer) when
// the input is unset so the repo layer writes SQL NULL.
func validateHostPath(in *string) (*string, error) {
	if in == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*in)
	if v == "" {
		// Treat explicit empty string the same as unset — the operator
		// most likely meant "no bind, use named volume".
		return nil, nil
	}
	if len(v) > maxPathLen {
		return nil, fmt.Errorf("invalid_host_path: max %d chars", maxPathLen)
	}
	if !strings.HasPrefix(v, "/") {
		return nil, errors.New("invalid_host_path: must be absolute (start with /)")
	}
	if containsParentSegment(v) {
		return nil, errors.New("invalid_host_path: must not contain '..' segments")
	}
	return &v, nil
}

// validatePairing makes sure the (is_named, host_path) pair is internally
// consistent. Named volumes are docker-managed and must NOT carry a host
// path; bind mounts are operator-managed and MUST carry one. The deploy
// engine relies on this invariant, so we enforce it at the seam.
func validatePairing(isNamed bool, host *string) error {
	if isNamed {
		if host != nil {
			return errors.New("invalid_host_path: must be empty for named volumes")
		}
		return nil
	}
	if host == nil || strings.TrimSpace(*host) == "" {
		return errors.New("invalid_host_path: required when is_named=false")
	}
	return nil
}

// normaliseService trims, validates, and returns a *string suitable for
// the repo layer (nil = single-container app).
func normaliseService(in *string) (*string, error) {
	if in == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*in)
	if v == "" {
		return nil, nil
	}
	if !serviceRegex.MatchString(v) {
		return nil, fmt.Errorf("invalid_service: %q (must match [a-zA-Z0-9][a-zA-Z0-9._-]*)", v)
	}
	return &v, nil
}

// containsParentSegment reports whether the path contains a `..` segment
// when split on `/`. We don't use filepath.Clean because that would
// silently normalise the path; we want to reject the input outright.
func containsParentSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}
