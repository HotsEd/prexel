package auth

import (
	"errors"
	"testing"
)

func TestValidateStrong(t *testing.T) {
	cases := []struct {
		name    string
		pw      string
		wantErr bool
	}{
		{"valid mixed", "Strong#Pass1ord", false},
		{"too short", "Aa1!aaaa", true},
		{"missing upper", "weak#password1word", true},
		{"missing digit", "WeakPassword#word", true},
		{"missing symbol", "WeakPassword1word", true},
		{"empty", "", true},
		{"exactly 12 ok", "Aaaaaaaaaa1!", false},
		{"11 chars rejected", "Aaaaaaaaa1!", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateStrong(tc.pw)
			if (err != nil) != tc.wantErr {
				t.Errorf("got err=%v want err=%v", err, tc.wantErr)
			}
			if tc.wantErr && err != nil && !errors.Is(err, ErrWeakPassword) {
				t.Errorf("expected ErrWeakPassword, got %v", err)
			}
		})
	}
}
