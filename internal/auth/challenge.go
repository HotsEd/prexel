package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ChallengeTTL is the lifetime of a 2FA challenge. Long enough for a user to
// fish their phone out of their pocket, short enough that a stolen challenge
// ID can't be replayed days later.
const ChallengeTTL = 5 * time.Minute

// MaxChallengeAttempts caps the number of TOTP/recovery tries against a
// single challenge. Beyond this the challenge is wiped and the user must
// restart from /auth/login (which costs another bcrypt round).
const MaxChallengeAttempts = 5

// Challenge errors.
var (
	ErrChallengeNotFound  = errors.New("auth: challenge not found")
	ErrChallengeExpired   = errors.New("auth: challenge expired")
	ErrChallengeExhausted = errors.New("auth: challenge attempts exhausted")
)

// Challenge represents a pending 2FA exchange after a successful password
// step. The store keeps these in memory only — they don't survive restarts.
// That's fine because the user can simply re-login.
type Challenge struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
	Attempts  int
}

// ChallengeStore is an in-memory map of pending 2FA challenges. Safe for
// concurrent use; a background goroutine started by StartCleanup sweeps
// expired entries every minute.
type ChallengeStore struct {
	mu    sync.Mutex
	items map[string]*Challenge
	now   func() time.Time // override-able for tests
}

// NewChallengeStore returns an empty store.
func NewChallengeStore() *ChallengeStore {
	return &ChallengeStore{
		items: make(map[string]*Challenge),
		now:   time.Now,
	}
}

// Create registers a fresh challenge for userID and returns it. The caller
// hands the ID back to the client; the secret never leaves the server.
func (s *ChallengeStore) Create(userID string) *Challenge {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &Challenge{
		ID:        uuid.NewString(),
		UserID:    userID,
		ExpiresAt: s.now().Add(ChallengeTTL),
	}
	s.items[c.ID] = c
	return c
}

// Get returns the challenge without consuming it, so the handler can record
// failed attempts. Errors when missing or expired (expired entries are also
// evicted on the way out to keep the map tidy).
func (s *ChallengeStore) Get(id string) (*Challenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.items[id]
	if !ok {
		return nil, ErrChallengeNotFound
	}
	if s.now().After(c.ExpiresAt) {
		delete(s.items, id)
		return nil, ErrChallengeExpired
	}
	return c, nil
}

// IncrementAttempts bumps the counter. If the limit is reached the challenge
// is removed and ErrChallengeExhausted is returned — the caller should treat
// that as a hard 401 and force the user back to /auth/login.
func (s *ChallengeStore) IncrementAttempts(id string) (*Challenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.items[id]
	if !ok {
		return nil, ErrChallengeNotFound
	}
	if s.now().After(c.ExpiresAt) {
		delete(s.items, id)
		return nil, ErrChallengeExpired
	}
	c.Attempts++
	if c.Attempts >= MaxChallengeAttempts {
		delete(s.items, id)
		return c, ErrChallengeExhausted
	}
	return c, nil
}

// Consume removes the challenge and returns it. Used on a successful TOTP /
// recovery-code verification — the ID is single-use.
func (s *ChallengeStore) Consume(id string) (*Challenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.items[id]
	if !ok {
		return nil, ErrChallengeNotFound
	}
	delete(s.items, id)
	if s.now().After(c.ExpiresAt) {
		return nil, ErrChallengeExpired
	}
	return c, nil
}

// cleanup walks the map and drops expired entries. Cheap because the map
// is small (one entry per active login attempt).
func (s *ChallengeStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, c := range s.items {
		if now.After(c.ExpiresAt) {
			delete(s.items, id)
		}
	}
}

// StartCleanup launches a goroutine that periodically removes expired
// challenges. Returns when ctx is cancelled.
func (s *ChallengeStore) StartCleanup(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.cleanup()
			}
		}
	}()
}
