// Package provider resolves env vars and account directories, merging
// built-in presets with user config (ADR-0002).
package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/config"
)

// Presets are the env vars for providers aiacc knows out of the box.
var Presets = map[string]string{
	"claude": "CLAUDE_CONFIG_DIR",
}

var (
	ErrUnknownProvider = errors.New("unknown provider")
	ErrUnknownAccount  = errors.New("unknown account")
)

// EnvVar returns the environment variable a provider switches on. User config
// overrides the preset; a provider known by neither is an error.
func EnvVar(c *config.Config, provider string) (string, error) {
	if p, ok := c.Providers[provider]; ok && p.EnvVar != "" {
		return p.EnvVar, nil
	}
	if env, ok := Presets[provider]; ok {
		return env, nil
	}
	return "", ErrUnknownProvider
}

// AccountDir returns the account's directory with a leading ~ expanded.
func AccountDir(c *config.Config, provider, account string) (string, error) {
	p, ok := c.Providers[provider]
	if !ok {
		return "", ErrUnknownProvider
	}
	a, ok := p.Accounts[account]
	if !ok {
		return "", ErrUnknownAccount
	}
	return expandHome(a.Dir)
}

func expandHome(dir string) (string, error) {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(dir, "~")), nil
	}
	return dir, nil
}
