package auth

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestGenerateRecoveryCodes_ReturnsEightUniqueCodes(t *testing.T) {
	plain, hashed, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(plain) != RecoveryCodeCount || len(hashed) != RecoveryCodeCount {
		t.Fatalf("expected %d codes, got plain=%d hashed=%d", RecoveryCodeCount, len(plain), len(hashed))
	}
	seen := map[string]struct{}{}
	for i, c := range plain {
		// Format: XXXX-XXXX-XX
		if strings.Count(c, "-") != 2 {
			t.Errorf("bad format at %d: %s", i, c)
		}
		if _, dup := seen[c]; dup {
			t.Errorf("duplicate code: %s", c)
		}
		seen[c] = struct{}{}

		// Each stored hash must be a bcrypt string and must verify
		// against the normalised plaintext (uppercase, no dashes).
		if !strings.HasPrefix(hashed[i], "$2a$") && !strings.HasPrefix(hashed[i], "$2b$") {
			t.Errorf("hash %d is not a bcrypt string: %q", i, hashed[i])
		}
		norm := strings.ReplaceAll(strings.ToUpper(c), "-", "")
		if err := bcrypt.CompareHashAndPassword([]byte(hashed[i]), []byte(norm)); err != nil {
			t.Errorf("hash %d does not verify against its plaintext: %v", i, err)
		}
	}
}

func TestVerifyAndConsumeRecoveryCode_OneShot(t *testing.T) {
	plain, hashed, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	pick := plain[3]

	remaining, ok := VerifyAndConsumeRecoveryCode(hashed, pick)
	if !ok {
		t.Fatal("valid code rejected")
	}
	if len(remaining) != RecoveryCodeCount-1 {
		t.Errorf("expected %d remaining, got %d", RecoveryCodeCount-1, len(remaining))
	}

	// Second consume of the same code must fail.
	_, ok = VerifyAndConsumeRecoveryCode(remaining, pick)
	if ok {
		t.Error("consumed code accepted twice")
	}
}

func TestVerifyAndConsumeRecoveryCode_NormalizesInput(t *testing.T) {
	plain, hashed, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Lowercase + extra whitespace should still match.
	with := "  " + strings.ToLower(plain[0]) + "\n"
	_, ok := VerifyAndConsumeRecoveryCode(hashed, with)
	if !ok {
		t.Errorf("normalized code rejected: %q", with)
	}
}

func TestVerifyAndConsumeRecoveryCode_RejectsInvalid(t *testing.T) {
	_, hashed, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	remaining, ok := VerifyAndConsumeRecoveryCode(hashed, "AAAA-BBBB-CC")
	if ok {
		t.Error("garbage code accepted")
	}
	if len(remaining) != len(hashed) {
		t.Errorf("list mutated on failed verify: %d vs %d", len(remaining), len(hashed))
	}
}

func TestVerifyAndConsumeRecoveryCode_EmptyInputs(t *testing.T) {
	if _, ok := VerifyAndConsumeRecoveryCode(nil, "ABCD-EFGH-IJ"); ok {
		t.Error("accepted with nil hash list")
	}
	if _, ok := VerifyAndConsumeRecoveryCode([]string{"deadbeef"}, ""); ok {
		t.Error("accepted empty code")
	}
}
