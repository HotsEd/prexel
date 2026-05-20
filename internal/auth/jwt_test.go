package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const jwtTestSecret = "test-jwt-secret-32-chars-minimum-aaaaaa"

func TestIssueAccessToken_RejectsShortSecret(t *testing.T) {
	if _, _, err := IssueAccessToken("short", "u1"); err == nil {
		t.Error("expected error for short secret")
	}
}

func TestIssueAndParse_RoundTrip(t *testing.T) {
	tok, exp, err := IssueAccessToken(jwtTestSecret, "user-123")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	if time.Until(exp) <= 0 || time.Until(exp) > AccessTokenTTL+time.Second {
		t.Errorf("unexpected expiry: %v", time.Until(exp))
	}
	sub, err := ParseAccessToken(jwtTestSecret, tok)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if sub != "user-123" {
		t.Errorf("sub mismatch: got %q want user-123", sub)
	}
}

func TestParseAccessToken_RejectsBadSignature(t *testing.T) {
	tok, _, _ := IssueAccessToken(jwtTestSecret, "u1")
	if _, err := ParseAccessToken("another-secret-32-chars-minimum-bbbbbb", tok); err == nil {
		t.Error("expected error for wrong secret")
	}
}

func TestParseAccessToken_RejectsMalformed(t *testing.T) {
	if _, err := ParseAccessToken(jwtTestSecret, "not.a.jwt"); err == nil {
		t.Error("expected error for malformed token")
	}
	if _, err := ParseAccessToken(jwtTestSecret, ""); err == nil {
		t.Error("expected error for empty token")
	}
}

func TestParseAccessToken_RejectsExpired(t *testing.T) {
	// Manually mint an expired token with the same secret.
	claims := jwt.MapClaims{
		"sub": "u1",
		"iss": JWTIssuer,
		"iat": time.Now().Add(-time.Hour).Unix(),
		"exp": time.Now().Add(-time.Minute).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(jwtTestSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := ParseAccessToken(jwtTestSecret, signed); err == nil {
		t.Error("expected expired token to be rejected")
	}
}

func TestParseAccessToken_RejectsWrongAlg(t *testing.T) {
	// "none" alg should be rejected because of method check.
	claims := jwt.MapClaims{"sub": "u1", "iss": JWTIssuer, "exp": time.Now().Add(time.Hour).Unix()}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	if _, err := ParseAccessToken(jwtTestSecret, signed); err == nil {
		t.Error("expected non-HS256 algorithm to be rejected")
	}
}

func TestParseAccessToken_RejectsMissingSub(t *testing.T) {
	claims := jwt.MapClaims{"iss": JWTIssuer, "exp": time.Now().Add(time.Hour).Unix()}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := tok.SignedString([]byte(jwtTestSecret))
	if _, err := ParseAccessToken(jwtTestSecret, signed); err == nil || !strings.Contains(err.Error(), "sub") {
		t.Errorf("expected missing sub error, got %v", err)
	}
}

func TestParseAccessToken_RejectsMissingIssuer(t *testing.T) {
	// Token without the iss claim — older Prexel builds, foreign issuers.
	claims := jwt.MapClaims{
		"sub": "u1",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := tok.SignedString([]byte(jwtTestSecret))
	if _, err := ParseAccessToken(jwtTestSecret, signed); !errors.Is(err, ErrWrongIssuer) {
		t.Errorf("expected ErrWrongIssuer, got %v", err)
	}
}

func TestParseAccessToken_RejectsWrongIssuer(t *testing.T) {
	// Token with a different iss claim — sibling service sharing the
	// HS256 secret must not be able to authenticate as a Prexel user.
	claims := jwt.MapClaims{
		"sub": "u1",
		"iss": "other-service",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := tok.SignedString([]byte(jwtTestSecret))
	if _, err := ParseAccessToken(jwtTestSecret, signed); !errors.Is(err, ErrWrongIssuer) {
		t.Errorf("expected ErrWrongIssuer for foreign issuer, got %v", err)
	}
}
