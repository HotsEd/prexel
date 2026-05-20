package auth

import (
	"errors"
	"testing"
	"time"
)

func TestChallengeStore_CreateAndConsume(t *testing.T) {
	s := NewChallengeStore()
	c := s.Create("user-1")
	if c.ID == "" {
		t.Fatal("empty challenge id")
	}
	if c.UserID != "user-1" {
		t.Errorf("user id mismatch: %s", c.UserID)
	}

	got, err := s.Consume(c.ID)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got.UserID != "user-1" {
		t.Errorf("consume returned wrong user: %s", got.UserID)
	}

	// Second consume fails.
	if _, err := s.Consume(c.ID); !errors.Is(err, ErrChallengeNotFound) {
		t.Errorf("expected not_found, got %v", err)
	}
}

func TestChallengeStore_Expires(t *testing.T) {
	s := NewChallengeStore()
	current := time.Now()
	s.now = func() time.Time { return current }

	c := s.Create("user-1")
	// Jump past the TTL.
	current = current.Add(ChallengeTTL + time.Second)

	if _, err := s.Get(c.ID); !errors.Is(err, ErrChallengeExpired) {
		t.Errorf("expected expired, got %v", err)
	}
}

func TestChallengeStore_AttemptsLimit(t *testing.T) {
	s := NewChallengeStore()
	c := s.Create("user-1")
	for i := 0; i < MaxChallengeAttempts-1; i++ {
		if _, err := s.IncrementAttempts(c.ID); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// Final increment exhausts and removes.
	if _, err := s.IncrementAttempts(c.ID); !errors.Is(err, ErrChallengeExhausted) {
		t.Errorf("expected exhausted, got %v", err)
	}
	if _, err := s.Get(c.ID); !errors.Is(err, ErrChallengeNotFound) {
		t.Errorf("exhausted challenge should be deleted, got %v", err)
	}
}

func TestChallengeStore_CleanupRemovesExpired(t *testing.T) {
	s := NewChallengeStore()
	current := time.Now()
	s.now = func() time.Time { return current }

	c1 := s.Create("user-1")
	current = current.Add(ChallengeTTL + time.Second)
	c2 := s.Create("user-2") // fresh

	s.cleanup()

	if _, err := s.Get(c1.ID); !errors.Is(err, ErrChallengeNotFound) {
		t.Errorf("expired challenge survived cleanup: %v", err)
	}
	if _, err := s.Get(c2.ID); err != nil {
		t.Errorf("fresh challenge wrongly evicted: %v", err)
	}
}
