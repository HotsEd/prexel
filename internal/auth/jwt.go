package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AccessTokenTTL is the lifetime of issued access tokens.
const AccessTokenTTL = 15 * time.Minute

// JWTIssuer is the value placed in the `iss` claim of every access token
// minted by Prexel. ParseAccessToken refuses to accept tokens that don't
// carry this exact issuer, which prevents tokens minted by a sibling
// service that happens to share the HS256 secret from being honored here.
const JWTIssuer = "prexel"

// ErrWrongIssuer is returned by ParseAccessToken when the token is
// well-formed and signed correctly but carries an `iss` claim that
// isn't JWTIssuer (or no iss claim at all).
var ErrWrongIssuer = errors.New("auth: wrong jwt issuer")

// IssueAccessToken signs a short-lived HS256 JWT carrying the user id as `sub`
// and Prexel's well-known issuer as `iss`. Returns the compact token, its
// absolute expiry, and an error.
func IssueAccessToken(secret, userID string) (string, time.Time, error) {
	if len(secret) < 32 {
		return "", time.Time{}, errors.New("auth: jwt secret too short")
	}
	now := time.Now()
	exp := now.Add(AccessTokenTTL)
	claims := jwt.MapClaims{
		"sub": userID,
		"iss": JWTIssuer,
		"iat": now.Unix(),
		"exp": exp.Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign: %w", err)
	}
	return signed, exp, nil
}

// ParseAccessToken verifies an HS256 JWT and returns the `sub` claim. It
// also enforces that the `iss` claim equals JWTIssuer — tokens minted by
// any other service (or older Prexel builds that did not stamp `iss`) are
// rejected with ErrWrongIssuer.
func ParseAccessToken(secret, raw string) (string, error) {
	tok, err := jwt.Parse(raw, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	})
	if err != nil || !tok.Valid {
		return "", errors.New("auth: invalid token")
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("auth: invalid claims")
	}
	// Issuer check — distinct error so callers can tell "rejected because
	// of the iss claim" apart from "garbage token" in their logs.
	iss, _ := claims["iss"].(string)
	if iss != JWTIssuer {
		return "", ErrWrongIssuer
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", errors.New("auth: missing sub claim")
	}
	return sub, nil
}
