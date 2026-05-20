package auth

import (
	"errors"
	"unicode"
)

// ErrWeakPassword is returned when a password does not meet the strength policy.
var ErrWeakPassword = errors.New("password does not meet strength requirements")

// ValidateStrong enforces the Setup&Auth spec policy: minimum 12 characters,
// at least one uppercase letter, one digit, and one symbol (non-alphanumeric).
func ValidateStrong(pw string) error {
	if len(pw) < 12 {
		return ErrWeakPassword
	}
	var hasUpper, hasDigit, hasSymbol bool
	for _, r := range pw {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			hasSymbol = true
		}
	}
	if !hasUpper || !hasDigit || !hasSymbol {
		return ErrWeakPassword
	}
	return nil
}
