// Package provider resolves env vars and account directories, merging
// built-in presets with user config (ADR-0002).
package provider

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/config"
)

// Preset is a built-in provider: the env var its CLI reads to select a config
// directory, the command aiacc launches for it, the directory that CLI uses when
// no env var is set, and the entries under that directory that hold *authored*
// assets rather than identity or usage state.
type Preset struct {
	EnvVar  string
	Command string
	BaseDir string   // the CLI's own default config dir (source of shared assets)
	Shared  []string // entries under BaseDir shared across every account
	State   []string // entries under BaseDir that are per-account state, never shared

	// Installs are where the CLI's own installers put Command outside the usual
	// bin dirs, so PATH may not reach it.
	Installs []string
}

// Presets are the AI CLIs aiacc knows out of the box. Each isolates accounts by
// pointing a config-dir env var at a directory, then runs its command there.
// Any other such CLI works with no preset: register it with a custom env var and
// command (see `aiacc add --env --command`).
//
// Shared lists only authoring surfaces — skills, sub-agents, slash commands,
// hooks, instructions, settings. Credentials, transcripts, and usage counters are
// deliberately absent: isolating those is the whole point of a profile.
//
// State lists the opposite: directories the CLI keeps per account, which must
// never be shared. Naming them is what lets Unclassified tell a *new* config
// surface from the state we already decided about — without it, a directory the
// CLI adds in a future version is indistinguishable from a cache, and profiles
// miss it in silence (the way `output-styles/` was missed until someone noticed).
var Presets = map[string]Preset{
	"claude": {
		EnvVar:  "CLAUDE_CONFIG_DIR",
		Command: "claude",
		BaseDir: "~/.claude",
		Shared: []string{
			"CLAUDE.md",
			"agents",
			"commands",
			"skills",
			"hooks",
			"output-styles",
			"plugins",
			"settings.json",
		},
		Installs: []string{"~/.claude/local/claude"}, // `claude migrate-installer`
		State: []string{
			"backups", "cache", "chrome", "daemon", "debug", "file-history",
			"ide", "jobs", "local", "paste-cache", "projects", "security",
			"session-env", "sessions", "shell-snapshots", "statsig",
			"telemetry", "todos", "uploads",
		},
	},
	"codex": {
		EnvVar:  "CODEX_HOME",
		Command: "codex",
		BaseDir: "~/.codex",
		Shared:  []string{"AGENTS.md", "prompts"},
		State:   []string{"cache", "log", "sessions"},
	},
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

// Shared returns the provider's shared-asset source directory (expanded) and the
// entry names to share from it. ok is false for a provider aiacc has no preset
// for — a custom CLI whose asset layout aiacc cannot know, so nothing is shared.
func Shared(provider string) (baseDir string, entries []string, ok bool) {
	p, found := Presets[provider]
	if !found || p.BaseDir == "" || len(p.Shared) == 0 {
		return "", nil, false
	}
	dir, err := expandHome(p.BaseDir)
	if err != nil {
		return "", nil, false
	}
	return dir, p.Shared, true
}

// Unclassified returns a provider's base config dir (expanded) and the
// directories under it that are neither shared nor known per-account state,
// sorted.
//
// This is the drift check: aiacc's shared list is hardcoded, so a config surface
// the CLI adds later is silently absent from every profile until someone updates
// that list. Surfacing the unknown directory turns that silence into a question.
//
// Only directories are considered, and dot-entries are skipped: every config
// surface a CLI has added so far is a plain directory, while the files beside
// them are caches, logs and state that would bury the signal.
func Unclassified(provider string) (baseDir string, extra []string, ok bool) {
	p := Presets[provider]
	dir, _, ok := Shared(provider)
	if !ok {
		return "", nil, false
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, false
	}
	known := make(map[string]bool, len(p.Shared)+len(p.State))
	for _, n := range p.Shared {
		known[n] = true
	}
	for _, n := range p.State {
		known[n] = true
	}
	var out []string
	for _, e := range ents {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || known[name] {
			continue
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return dir, out, true
}

// Installs returns the preset's known out-of-PATH install locations, expanded.
func Installs(provider string) []string {
	var out []string
	for _, p := range Presets[provider].Installs {
		if dir, err := expandHome(p); err == nil {
			out = append(out, dir)
		}
	}
	return out
}
