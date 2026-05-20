package auth

import (
	"crypto/rand"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// recoveryAlphabet is the base32-ish charset used for recovery codes. We avoid
// 0/O/1/I to stay readable when users print or hand-copy the codes.
const recoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// RecoveryCodeCount is the number of recovery codes generated per user. Eight
// is the de-facto industry default (matches GitHub, Google, etc.).
const RecoveryCodeCount = 8

// recoveryBcryptCost is the bcrypt work factor for recovery codes.
// Lower than BcryptCost (12) because:
//   - the recovery code space (~50 bits of CSPRNG entropy) is already
//     enormous compared to a human password, so the marginal hardening
//     from a higher work factor is small;
//   - the verification path runs INSIDE a 5-attempt challenge window,
//     so an attacker can't keep poking;
//   - 8 codes per user means GenerateRecoveryCodes does 8 hashes
//     up-front during the setup wizard. At cost 10 that's still ~2s on
//     a small box, which is the worst case operators see.
//
// Net: cost 10 keeps the GPU-grind defense bcrypt is famous for
// without making the wizard feel slow.
const recoveryBcryptCost = 10

// GenerateRecoveryCodes returns 8 fresh recovery codes in plaintext alongside
// their bcrypt hashes (in matching order). The plaintext list is shown to
// the user UMA ÚNICA VEZ; only the hashes are persisted.
//
// Format: `XXXX-XXXX-XX` — 10 alphabet chars (~50 bits of entropy) with
// dashes inserted as visual separators only. Comparison strips the dashes.
//
// Hash storage moved from SHA-256 to bcrypt in migration 014. Reason:
// SHA-256 is a fast hash — a leaked DB lets a GPU farm grind the 50-bit
// codes in seconds. Bcrypt makes the same DB leak cost ~CPU-hours per
// code, matching the protection profile we already apply to passwords.
func GenerateRecoveryCodes() (plain, hashed []string, err error) {
	plain = make([]string, RecoveryCodeCount)
	hashed = make([]string, RecoveryCodeCount)
	for i := 0; i < RecoveryCodeCount; i++ {
		code, e := randomRecoveryCode()
		if e != nil {
			return nil, nil, e
		}
		h, e := hashRecoveryCode(code)
		if e != nil {
			return nil, nil, e
		}
		plain[i] = code
		hashed[i] = h
	}
	return plain, hashed, nil
}

func randomRecoveryCode() (string, error) {
	const n = 10
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = recoveryAlphabet[int(b)%len(recoveryAlphabet)]
	}
	// XXXX-XXXX-XX
	return fmt.Sprintf("%s-%s-%s", out[0:4], out[4:8], out[8:10]), nil
}

// hashRecoveryCode normalises a code (uppercase, strip dashes/spaces) and
// returns its bcrypt hash. Internal helper — callers persist hashes from
// GenerateRecoveryCodes and verify via VerifyAndConsumeRecoveryCode.
func hashRecoveryCode(code string) (string, error) {
	norm := normalizeRecoveryCode(code)
	h, err := bcrypt.GenerateFromPassword([]byte(norm), recoveryBcryptCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt: %w", err)
	}
	return string(h), nil
}

func normalizeRecoveryCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return code
}

// VerifyAndConsumeRecoveryCode walks the stored bcrypt hashes looking for one
// that matches the user-supplied code. bcrypt's CompareHashAndPassword is
// constant-time per call, but we still iterate through ALL hashes (even after
// finding a match) so the wall-clock cost of a wrong code looks the same as a
// right one — no timing-leak on "how many recovery codes does this user have
// left". On hit it returns the remaining list (with the matched hash removed)
// and valid=true. On miss the original list is returned unchanged.
func VerifyAndConsumeRecoveryCode(hashes []string, code string) (remaining []string, valid bool) {
	if len(hashes) == 0 || code == "" {
		return hashes, false
	}
	norm := []byte(normalizeRecoveryCode(code))

	matchIdx := -1
	for i, h := range hashes {
		if bcrypt.CompareHashAndPassword([]byte(h), norm) == nil && matchIdx == -1 {
			matchIdx = i
			// We deliberately KEEP iterating after a hit so the
			// number of bcrypt comparisons (and hence the response
			// time) depends only on len(hashes), not on whether
			// the input matched or which slot it matched in.
		}
	}
	if matchIdx == -1 {
		return hashes, false
	}
	out := make([]string, 0, len(hashes)-1)
	out = append(out, hashes[:matchIdx]...)
	out = append(out, hashes[matchIdx+1:]...)
	return out, true
}
