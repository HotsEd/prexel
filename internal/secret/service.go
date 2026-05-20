package secret

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/crypto"
)

// keyRegex enforces standard env-var naming.
var keyRegex = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Service is the domain entry point for the secrets table.
type Service struct {
	repo   *repo
	cipher *crypto.Cipher
}

// NewService wires a Service.
func NewService(db *sql.DB, cipher *crypto.Cipher) *Service {
	return &Service{repo: newRepo(db), cipher: cipher}
}

// UpsertItem is a single payload entry in Upsert.
type UpsertItem struct {
	Value       string
	IsBuildTime bool
	IsMultiline bool
}

// List returns the masked metadata for every secret of an app. Plaintext
// values are NEVER returned by this method.
func (s *Service) List(ctx context.Context, appID string) ([]Secret, error) {
	out, err := s.repo.list(ctx, appID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Secret{}
	}
	return out, nil
}

// Upsert applies the entire batch in one shot. Each value is encrypted with
// internal/crypto before insertion.
func (s *Service) Upsert(ctx context.Context, appID string, items map[string]UpsertItem) error {
	if len(items) == 0 {
		return nil
	}
	for k, v := range items {
		if !keyRegex.MatchString(k) {
			return fmt.Errorf("invalid_key: %q (must match ^[A-Z][A-Z0-9_]*$)", k)
		}
		if v.Value == "" {
			return fmt.Errorf("empty_value: %q", k)
		}
	}
	for k, v := range items {
		enc, err := s.cipher.Encrypt([]byte(v.Value))
		if err != nil {
			return fmt.Errorf("encrypt %q: %w", k, err)
		}
		if err := s.repo.upsert(ctx, uuid.NewString(), appID, k, enc, v.IsBuildTime, v.IsMultiline); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes a single secret by key. Returns ErrNotFound when the key
// did not exist.
func (s *Service) Delete(ctx context.Context, appID, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("missing_key")
	}
	return s.repo.delete(ctx, appID, key)
}

// Resolve returns every secret for an app with the plaintext value attached.
// INTERNAL — only the Deploy Engine should call this.
func (s *Service) Resolve(ctx context.Context, appID string) ([]SecretWithValue, error) {
	return s.resolve(ctx, appID, nil)
}

// ResolveBuildTime returns the build-time-only secret map.
func (s *Service) ResolveBuildTime(ctx context.Context, appID string) (map[string]string, error) {
	t := true
	rows, err := s.resolve(ctx, appID, &t)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

// ResolveRuntime returns the runtime-only secret map.
func (s *Service) ResolveRuntime(ctx context.Context, appID string) (map[string]string, error) {
	f := false
	rows, err := s.resolve(ctx, appID, &f)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

func (s *Service) resolve(ctx context.Context, appID string, buildTimeFilter *bool) ([]SecretWithValue, error) {
	rows, err := s.repo.listWithValues(ctx, appID, buildTimeFilter)
	if err != nil {
		return nil, err
	}
	out := make([]SecretWithValue, 0, len(rows))
	for _, r := range rows {
		pt, err := s.cipher.Decrypt(r.Value)
		if err != nil {
			return nil, fmt.Errorf("decrypt %q: %w", r.Key, err)
		}
		out = append(out, SecretWithValue{
			Secret: r.Secret,
			Value:  string(pt),
		})
	}
	return out, nil
}
