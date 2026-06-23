// Package config persists aiacc providers and accounts as TOML.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Account struct {
	Dir   string `toml:"dir"`
	Quota int    `toml:"quota,omitempty"`
}

type Provider struct {
	EnvVar   string             `toml:"env_var"`
	Accounts map[string]Account `toml:"accounts"`
}

type Config struct {
	Providers map[string]Provider `toml:"providers"`
}

// DefaultPath returns the config file location, honoring XDG_CONFIG_HOME.
func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "aiacc", "config.toml"), nil
}

// Load reads the config; a missing file yields an empty, ready-to-use Config.
func Load(path string) (*Config, error) {
	c := &Config{Providers: map[string]Provider{}}
	if _, err := toml.DecodeFile(path, c); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return nil, err
	}
	if c.Providers == nil {
		c.Providers = map[string]Provider{}
	}
	return c, nil
}

// Save writes the config, creating the parent directory if needed.
func Save(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}
