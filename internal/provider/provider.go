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

// Preset is a built-in provider: the env var its CLI reads to select a config
// directory, and the command aiacc launches for it.
type Preset struct {
	EnvVar  string
	Command string
}

// Presets are the AI CLIs aiacc knows out of the box. Each isolates accounts by
// pointing a config-dir env var at a directory, then runs its command there.
// Any other such CLI works with no preset: register it with a custom env var and
// command (see `aiacc add --env --command`).
var Presets = map[string]Preset{
	"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"},
	"codex":  {EnvVar: "CODEX_HOME", Command: "codex"},
}

var (
	ErrUnknownProvider = errors.New("unknown provider")
	ErrUnknownAccount  = errors.New("unknown account")
)

// EnvVar returns the environment variable a provider selects its config with.
// User config overrides the preset; a provider known by neither is an error.
func EnvVar(c *config.Config, provider string) (string, error) {
	if p, ok := c.Providers[provider]; ok && p.EnvVar != "" {
		return p.EnvVar, nil
	}
	if pr, ok := Presets[provider]; ok {
		return pr.EnvVar, nil
	}
	return "", ErrUnknownProvider
}

// Command returns the CLI aiacc launches for a provider. Config overrides the
// preset. A registered provider with neither yields "" (it shows as "no
// launcher" until a command is set); an entirely unknown provider is an error.
func Command(c *config.Config, provider string) (string, error) {
	if p, ok := c.Providers[provider]; ok && p.Command != "" {
		return p.Command, nil
	}
	if pr, ok := Presets[provider]; ok {
		return pr.Command, nil
	}
	if _, ok := c.Providers[provider]; ok {
		return "", nil // registered, but no launch command yet
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
