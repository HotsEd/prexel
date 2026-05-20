package config

import (
	"strings"
	"testing"
)

// validSecret is 64 hex chars (32 bytes) — exactly what `openssl rand -hex 32`
// produces, which is what install.sh seeds prod machines with.
const validSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func baseConfig() *Config {
	return &Config{
		Env:       "production",
		Port:      3000,
		DataDir:   "/var/lib/prexel",
		SecretKey: validSecret,
		JWTSecret: validSecret,
	}
}

func TestValidate_ProductionRejectsExampleSecretKey(t *testing.T) {
	cases := []string{
		"replace-me-with-a-real-secret-replace-me-with-a-real-secret-1234",
		"REPLACE-ME-WITH-A-REAL-SECRET-REPLACE-ME-WITH-A-REAL-SECRET-1234",
		"dev-secret-key-0123456789abcdef0123456789abcdef0123456789abcdef0",
		"dev-some-other-value-padded-to-32-chars-padded-to-32-chars-12345",
		"change-me-please-change-me-please-change-me-please-change-me-12",
	}
	for _, secret := range cases {
		t.Run(secret[:12], func(t *testing.T) {
			c := baseConfig()
			c.SecretKey = secret
			err := c.validate()
			if err == nil {
				t.Fatalf("expected error for placeholder secret %q in production", secret)
			}
			if !strings.Contains(err.Error(), "PREXEL_SECRET_KEY") {
				t.Fatalf("error should mention PREXEL_SECRET_KEY; got %v", err)
			}
		})
	}
}

func TestValidate_ProductionRejectsExampleJWTSecret(t *testing.T) {
	c := baseConfig()
	c.JWTSecret = "dev-jwt-secret-0123456789abcdef0123456789abcdef0123456789abcdef"
	err := c.validate()
	if err == nil {
		t.Fatal("expected error for placeholder jwt secret in production")
	}
	if !strings.Contains(err.Error(), "PREXEL_JWT_SECRET") {
		t.Fatalf("error should mention PREXEL_JWT_SECRET; got %v", err)
	}
}

func TestValidate_ProductionAcceptsRealSecrets(t *testing.T) {
	c := baseConfig()
	if err := c.validate(); err != nil {
		t.Fatalf("expected production + real secrets to validate; got %v", err)
	}
}

func TestValidate_DevelopmentAllowsExampleSecrets(t *testing.T) {
	for _, env := range []string{"development", "dev"} {
		t.Run(env, func(t *testing.T) {
			c := baseConfig()
			c.Env = env
			c.SecretKey = "dev-secret-key-0123456789abcdef0123456789abcdef0123456789abcdef"
			c.JWTSecret = "replace-me-with-a-real-secret-replace-me-with-a-real-secret-12"
			if err := c.validate(); err != nil {
				t.Fatalf("dev mode should tolerate example secrets; got %v", err)
			}
		})
	}
}

func TestValidate_DevelopmentAcceptsRealSecrets(t *testing.T) {
	c := baseConfig()
	c.Env = "development"
	if err := c.validate(); err != nil {
		t.Fatalf("dev mode with real secrets should validate; got %v", err)
	}
}

func TestValidate_EmptyEnvTreatedAsDev(t *testing.T) {
	// Constructing the Config directly skips Load()'s defaults. The validator
	// should not surprise callers (e.g., tests, embedders) by failing on
	// placeholder secrets when no env has been declared.
	c := baseConfig()
	c.Env = ""
	c.SecretKey = "dev-secret-key-0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := c.validate(); err != nil {
		t.Fatalf("empty env should not be treated as production; got %v", err)
	}
}

func TestValidate_RejectsShortSecret(t *testing.T) {
	c := baseConfig()
	c.SecretKey = "tooshort"
	if err := c.validate(); err == nil {
		t.Fatal("expected length validation to reject short secret")
	}
}

func TestHasExamplePrefix(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"replace-me-now", true},
		{"REPLACE-ME-NOW", true},
		{"dev-secret-key-abcd", true},
		{"dev-foo", true},
		{"change-me", true},
		{"production-grade-secret", false},
		{"0123456789abcdef", false},
		{"", false},
	} {
		t.Run(tc.in, func(t *testing.T) {
			if got := hasExamplePrefix(tc.in); got != tc.want {
				t.Fatalf("hasExamplePrefix(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestExampleSecretPrefixes_Exported(t *testing.T) {
	// Loud canary: anyone editing the list should think about who else relies
	// on the prefixes (install.sh, example configs, docs).
	if len(ExampleSecretPrefixes) == 0 {
		t.Fatal("ExampleSecretPrefixes must not be empty")
	}
	for _, p := range ExampleSecretPrefixes {
		if p != strings.ToLower(p) {
			t.Fatalf("prefix %q should be lowercase to match hasExamplePrefix logic", p)
		}
	}
}
