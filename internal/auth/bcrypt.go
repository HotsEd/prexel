// Package auth holds password hashing, JWT issuance/parsing, refresh-token
// rotation with token families, and helpers for password change flows. See
// Confluence Tech Review §7–§10.
package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost is the bcrypt work factor. Confluence Setup&Auth spec mandates 12.
const BcryptCost = 12

// HashPassword returns a bcrypt hash of the given plaintext password.
func HashPassword(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("auth: empty password")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(plain), BcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword reports whether plain matches the stored bcrypt hash.
// Returns false for any error (including mismatch or malformed hash) so callers
// can use a single generic "invalid credentials" path.
func VerifyPassword(hash, plain string) bool {
	if hash == "" || plain == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
