// Package config loads runtime configuration from environment variables
// (prefix PREXEL_) using Viper, applies defaults, and validates required
// secret material at startup. See Confluence: Security & Communication.
package config

import (
	"fmt"
	"log"
	"strings"

	"github.com/spf13/viper"
)

// ExampleSecretPrefixes lists the case-insensitive prefixes that Prexel ships
// in example/dev configs. Anything that starts with one of these is considered
// a placeholder, not a real secret, and will be rejected when PREXEL_ENV is
// "production". Exported so tests (and any future linters) can stay in sync.
var ExampleSecretPrefixes = []string{
	"replace-me",
	"dev-secret-key",
	"dev-",
	"change-me",
}

// Config is the in-memory runtime configuration for the prexel server.
type Config struct {
	Env         string `mapstructure:"env"`
	Port        int    `mapstructure:"port"`
	DataDir     string `mapstructure:"data_dir"`
	SecretKey   string `mapstructure:"secret_key"`
	JWTSecret   string `mapstructure:"jwt_secret"`
	InstanceURL string `mapstructure:"instance_url"`

	// TLS — paths to PEM cert/key. If empty, a self-signed cert is generated
	// at first boot under DataDir/tls/.
	TLSCert string `mapstructure:"tls_cert"`
	TLSKey  string `mapstructure:"tls_key"`

	// GitHub App (optional, used by A5).
	GitHubAppID         string `mapstructure:"github_app_id"`
	GitHubAppPrivateKey string `mapstructure:"github_app_private_key"`
	GitHubAppSlug       string `mapstructure:"github_app_slug"`

	// TwoFactorIssuer is the label embedded in the otpauth URL — it's what
	// authenticator apps show next to the account name. Defaults to "Prexel".
	TwoFactorIssuer string `mapstructure:"two_factor_issuer"`

	// TrustedProxies is the CIDR allowlist of upstreams whose
	// X-Forwarded-For / X-Real-IP headers the rate-limiter will
	// honour. Empty (the default) means the limiter ignores the
	// headers entirely and always meters per real TCP peer — the
	// safe choice when Prexel is exposed directly. Operators behind
	// a reverse proxy MUST set this (e.g. "10.0.0.0/8,::1/128"),
	// otherwise every request will look like it comes from the
	// proxy's IP and rate-limiting collapses to "one bucket for
	// the whole world".
	//
	// Env: PREXEL_TRUSTED_PROXIES (comma-separated CIDRs or IPs).
	TrustedProxies []string `mapstructure:"trusted_proxies"`
}

// Load reads configuration from environment variables prefixed with PREXEL_.
// It fails hard if required secrets are missing or too short.
func Load() (*Config, error) {
	v := viper.New()
	v.SetEnvPrefix("PREXEL")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	v.SetDefault("env", "production")
	v.SetDefault("port", 3000)
	v.SetDefault("data_dir", "/var/lib/prexel")
	v.SetDefault("instance_url", "")
	v.SetDefault("two_factor_issuer", "Prexel")

	// Bind expected keys so AutomaticEnv picks them up reliably.
	_ = v.BindEnv("env", "PREXEL_ENV")
	_ = v.BindEnv("port", "PREXEL_PORT")
	_ = v.BindEnv("data_dir", "PREXEL_DATA_DIR")
	_ = v.BindEnv("secret_key", "PREXEL_SECRET_KEY")
	_ = v.BindEnv("jwt_secret", "PREXEL_JWT_SECRET")
	_ = v.BindEnv("instance_url", "PREXEL_INSTANCE_URL")
	_ = v.BindEnv("tls_cert", "PREXEL_TLS_CERT")
	_ = v.BindEnv("tls_key", "PREXEL_TLS_KEY")
	_ = v.BindEnv("github_app_id", "PREXEL_GITHUB_APP_ID")
	_ = v.BindEnv("github_app_private_key", "PREXEL_GITHUB_APP_PRIVATE_KEY")
	_ = v.BindEnv("github_app_slug", "PREXEL_GITHUB_APP_SLUG")
	_ = v.BindEnv("two_factor_issuer", "PREXEL_2FA_ISSUER")
	_ = v.BindEnv("trusted_proxies", "PREXEL_TRUSTED_PROXIES")

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("config unmarshal: %w", err)
	}

	// Viper hands us env var values as a single string; split into the
	// CIDR list the rate-limit middleware expects. Whitespace around
	// commas is tolerated.
	if raw := v.GetString("trusted_proxies"); raw != "" {
		parts := strings.Split(raw, ",")
		cfg.TrustedProxies = cfg.TrustedProxies[:0]
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				cfg.TrustedProxies = append(cfg.TrustedProxies, t)
			}
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if len(c.SecretKey) < 32 {
		return fmt.Errorf("PREXEL_SECRET_KEY must be at least 32 characters (got %d)", len(c.SecretKey))
	}
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("PREXEL_JWT_SECRET must be at least 32 characters (got %d)", len(c.JWTSecret))
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("PREXEL_PORT out of range: %d", c.Port)
	}
	if c.DataDir == "" {
		return fmt.Errorf("PREXEL_DATA_DIR must be set")
	}
	if err := c.validateSecretsNotExample(); err != nil {
		return err
	}
	return nil
}

// validateSecretsNotExample blocks startup in production when the operator
// left a placeholder secret behind. In dev (or when PREXEL_ENV is unset) we
// only warn, because the canonical dev configs intentionally ship with
// predictable values so devs can decrypt fixtures without rotating keys on
// every machine.
func (c *Config) validateSecretsNotExample() error {
	prod := !c.IsDevelopment() && c.Env != ""
	for _, check := range []struct {
		name, value string
	}{
		{"PREXEL_SECRET_KEY", c.SecretKey},
		{"PREXEL_JWT_SECRET", c.JWTSecret},
	} {
		if !hasExamplePrefix(check.value) {
			continue
		}
		if prod {
			return fmt.Errorf(
				"%s appears to be an example/placeholder value (starts with one of %v); "+
					"generate a real secret with `openssl rand -hex 32`",
				check.name, ExampleSecretPrefixes,
			)
		}
		log.Printf("warn: %s looks like an example value (%q...); "+
			"this is only allowed because PREXEL_ENV=%q",
			check.name, truncate(check.value, 12), c.Env)
	}
	return nil
}

// hasExamplePrefix reports whether v starts with any of ExampleSecretPrefixes
// (case-insensitive).
func hasExamplePrefix(v string) bool {
	lower := strings.ToLower(v)
	for _, p := range ExampleSecretPrefixes {
		if strings.HasPrefix(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// IsDevelopment reports whether the server is running in development mode.
func (c *Config) IsDevelopment() bool {
	return c.Env == "development" || c.Env == "dev"
}
