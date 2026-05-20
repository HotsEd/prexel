// Package config manages the client-side `~/.prexel/config.yaml` file used by
// the CLI subcommands (login, logout, deploy, etc.). The server-side runtime
// configuration is in internal/config — distinct concern, distinct file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the persisted client configuration.
type Config struct {
	InstanceURL    string      `yaml:"instance_url,omitempty"`
	AccessToken    string      `yaml:"access_token,omitempty"`
	RefreshToken   string      `yaml:"refresh_token,omitempty"`
	TokenExpiresAt time.Time   `yaml:"token_expires_at,omitempty"`
	KnownHosts     []KnownHost `yaml:"known_hosts,omitempty"`
}

// KnownHost pins a self-signed cert fingerprint for a hostname.
type KnownHost struct {
	Host        string `yaml:"host"`
	Fingerprint string `yaml:"fingerprint"`
}

// Path returns the absolute path of the config file. It honours
// PREXEL_CLI_CONFIG_DIR for tests and the prexel-cli compose volume.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// Dir returns the resolved client config directory and ensures it exists with
// mode 0700.
func Dir() (string, error) {
	if v := os.Getenv("PREXEL_CLI_CONFIG_DIR"); v != "" {
		if err := os.MkdirAll(v, 0o700); err != nil {
			return "", err
		}
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".prexel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// Load reads the config file. Returns an empty Config (not an error) if the
// file is absent — first-run is normal.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &c, nil
}

// Save writes the config atomically with mode 0600.
func Save(c *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write config tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	// Ensure mode is correct even if the file already existed.
	_ = os.Chmod(path, 0o600)
	return nil
}

// AddKnownHost upserts a known host fingerprint.
func (c *Config) AddKnownHost(host, fingerprint string) {
	for i := range c.KnownHosts {
		if c.KnownHosts[i].Host == host {
			c.KnownHosts[i].Fingerprint = fingerprint
			return
		}
	}
	c.KnownHosts = append(c.KnownHosts, KnownHost{Host: host, Fingerprint: fingerprint})
}

// FindKnownHost returns the recorded fingerprint and whether one was found.
func (c *Config) FindKnownHost(host string) (string, bool) {
	for _, h := range c.KnownHosts {
		if h.Host == host {
			return h.Fingerprint, true
		}
	}
	return "", false
}

// ClearTokens wipes the auth state but keeps instance_url and known_hosts.
func (c *Config) ClearTokens() {
	c.AccessToken = ""
	c.RefreshToken = ""
	c.TokenExpiresAt = time.Time{}
}

// HasAuth reports whether an access token (possibly expired) is recorded.
func (c *Config) HasAuth() bool {
	return c.AccessToken != ""
}
