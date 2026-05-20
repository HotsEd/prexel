package tag

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// nameRegex constrains tag names AFTER normalisation. Letters/digits/dashes
// only, must begin with letter or digit (so we never accept "-foo"), 1-40
// chars. The migration's UNIQUE constraint is on the raw stored value;
// normalising in the service means two operators typing "Prod" and "prod"
// end up sharing the same row instead of fighting over it.
var nameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// colorRegex is the hex-colour shape used by the UI tags pill. We allow both
// cases for the digits but always store what the operator sent — the front-
// end is lowercasing anyway, so re-canonicalising here would be cosmetic
// churn that breaks the "what you set is what comes back" contract.
var colorRegex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// wsRegex collapses any run of whitespace into a single dash during
// normalisation. Used so a user's "qa stage" becomes "qa-stage" and a
// stray double-space doesn't yield "qa--stage".
var wsRegex = regexp.MustCompile(`\s+`)

// Service is the domain entry point for the tags + app_tags tables.
type Service struct {
	repo *repo
}

// NewService wires a Service backed by the given DB handle.
func NewService(db *sql.DB) *Service {
	return &Service{repo: newRepo(db)}
}

// Create is upsert-by-name: if a tag with the same normalised name already
// exists, we return it instead of failing with ErrNameInUse. The HTTP layer
// can therefore treat "POST /tags" as idempotent without re-implementing
// the get-or-create dance.
//
// `color` is optional. When set it must match `^#[0-9a-fA-F]{6}$`; existing
// rows keep their existing colour (we don't overwrite on upsert — the first
// writer wins, which is the least-surprising behaviour for a dictionary).
func (s *Service) Create(ctx context.Context, rawName string, color *string) (*Tag, error) {
	name, err := normalizeName(rawName)
	if err != nil {
		return nil, err
	}
	colorPtr, err := normalizeColor(color)
	if err != nil {
		return nil, err
	}

	t := &Tag{
		ID:    uuid.NewString(),
		Name:  name,
		Color: colorPtr,
	}
	err = s.repo.insert(ctx, t)
	if errors.Is(err, ErrNameInUse) {
		// Upsert path: someone else already created a tag with this
		// name. Return their row so callers don't have to special-case
		// the conflict.
		return s.repo.getByName(ctx, name)
	}
	if err != nil {
		return nil, err
	}
	return s.repo.get(ctx, t.ID)
}

// Get resolves a tag by id.
func (s *Service) Get(ctx context.Context, id string) (*Tag, error) {
	return s.repo.get(ctx, strings.TrimSpace(id))
}

// List returns every tag in the dictionary, ordered by name.
func (s *Service) List(ctx context.Context) ([]Tag, error) {
	return s.repo.list(ctx)
}

// Delete removes a tag and (via ON DELETE CASCADE on app_tags) every
// attachment. Returns ErrNotFound when the id does not exist so the HTTP
// layer can map it to 404 without inspecting RowsAffected.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.delete(ctx, strings.TrimSpace(id))
}

// Attach links an existing tag to an existing app. Idempotent — re-attaching
// the same pair is silently a no-op, which lets callers retry without
// special-casing "already attached".
func (s *Service) Attach(ctx context.Context, appID, tagID string) error {
	return s.repo.attach(ctx, appID, tagID)
}

// Detach removes a single (app, tag) link. Missing rows are not an error.
func (s *Service) Detach(ctx context.Context, appID, tagID string) error {
	return s.repo.detach(ctx, appID, tagID)
}

// ForApp returns the *names* of tags attached to an app. We expose names
// rather than full Tag rows because the SetAppTags input is name-keyed and
// every UI usage so far ("show the chips on an app card") only needs names
// plus colour. Callers that need full rows can use ForAppDetailed.
func (s *Service) ForApp(ctx context.Context, appID string) ([]string, error) {
	rows, err := s.repo.listForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out, nil
}

// ForAppDetailed is the full-Tag variant of ForApp, used when the caller
// also wants the colour (dashboard chip rendering).
func (s *Service) ForAppDetailed(ctx context.Context, appID string) ([]Tag, error) {
	return s.repo.listForApp(ctx, appID)
}

// AppsForTag is the inverse lookup — every app id carrying a given tag.
// Drives the dashboard's "show me prod apps" filter.
func (s *Service) AppsForTag(ctx context.Context, tagID string) ([]string, error) {
	return s.repo.listAppsForTag(ctx, strings.TrimSpace(tagID))
}

// SetAppTags replaces the full set of tags attached to an app, given the
// desired set of *names*. The operation is idempotent: missing tags are
// created on the fly (same upsert rule as Create), extra ones are detached,
// and the existing intersection is left untouched. The returned slice is
// the post-state, sorted by name.
//
// We deliberately key on names (not ids) because the most common caller is
// a PUT from the SPA with a free-text chip input — the UI doesn't know
// internal ids for new tags, and forcing a two-step "POST then PUT" dance
// would surface that incoherence to the operator.
func (s *Service) SetAppTags(ctx context.Context, appID string, names []string) ([]Tag, error) {
	// Normalise + de-duplicate the requested names. We keep insertion
	// order during dedup so an operator's tag input doesn't get
	// reshuffled before we hit the DB (cosmetic but worth it).
	normalized := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, raw := range names {
		n, err := normalizeName(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		normalized = append(normalized, n)
	}

	// Resolve names → tag rows, creating any that don't exist yet.
	// Done one-by-one because the volume is tiny (operators don't put
	// 1000 tags on an app) and the upsert path inside Create already
	// handles the race condition.
	desired := make(map[string]Tag, len(normalized))
	for _, n := range normalized {
		t, err := s.Create(ctx, n, nil)
		if err != nil {
			return nil, fmt.Errorf("ensure tag %q: %w", n, err)
		}
		desired[t.ID] = *t
	}

	// Diff against the current attachment set so we only mutate the
	// rows that actually changed.
	current, err := s.repo.listForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	currentByID := make(map[string]struct{}, len(current))
	for _, t := range current {
		currentByID[t.ID] = struct{}{}
	}

	// Detach the ones the caller dropped.
	for id := range currentByID {
		if _, keep := desired[id]; !keep {
			if err := s.repo.detach(ctx, appID, id); err != nil {
				return nil, fmt.Errorf("detach %s: %w", id, err)
			}
		}
	}
	// Attach the ones that are new (or already present — attach is
	// idempotent so we don't bother diffing for it).
	for id := range desired {
		if err := s.repo.attach(ctx, appID, id); err != nil {
			return nil, fmt.Errorf("attach %s: %w", id, err)
		}
	}

	return s.repo.listForApp(ctx, appID)
}

// normalizeName runs the tag-name normalisation pipeline:
//
//  1. Trim leading/trailing whitespace.
//  2. Lowercase (so "Prod" and "prod" collide on the UNIQUE index).
//  3. Collapse internal whitespace runs into single dashes.
//  4. Strip anything that isn't [a-z0-9-] (Unicode, punctuation, …).
//  5. Validate the result against nameRegex + length bounds.
//
// We do all this in the service layer (not the repo) so the constraint
// applies uniformly across HTTP, SetAppTags' implicit Create, and any
// future bulk-import path.
func normalizeName(in string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(in))
	s = wsRegex.ReplaceAllString(s, "-")
	// Strip characters that are neither [a-z0-9] nor '-'. A regex
	// pass is shorter but less obvious; a single byte-loop both
	// documents intent and avoids re-compiling a throw-away pattern.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-':
			b.WriteRune(r)
		}
	}
	s = b.String()
	if s == "" {
		return "", errors.New("invalid_name: empty after normalisation")
	}
	if len(s) > 40 {
		return "", fmt.Errorf("invalid_name: %d chars (max 40)", len(s))
	}
	if !nameRegex.MatchString(s) {
		return "", fmt.Errorf("invalid_name: %q must match ^[a-z0-9][a-z0-9-]*$", s)
	}
	return s, nil
}

// normalizeColor validates the optional hex colour. A nil or empty input
// stores NULL — the UI then picks a default chip colour, which is what we
// want for "operator didn't care".
func normalizeColor(in *string) (*string, error) {
	if in == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*in)
	if v == "" {
		return nil, nil
	}
	if !colorRegex.MatchString(v) {
		return nil, fmt.Errorf("invalid_color: %q must match ^#[0-9a-fA-F]{6}$", v)
	}
	return &v, nil
}
