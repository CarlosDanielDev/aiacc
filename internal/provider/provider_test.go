package provider

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
)

func TestEnvVarFromPresetWhenAbsent(t *testing.T) {
	c := &config.Config{Providers: map[string]config.Provider{}}
	got, err := EnvVar(c, "claude")
	if err != nil || got != "CLAUDE_CONFIG_DIR" {
		t.Fatalf("EnvVar = %q, %v; want CLAUDE_CONFIG_DIR", got, err)
	}
}

func TestEnvVarUnknownProvider(t *testing.T) {
	c := &config.Config{Providers: map[string]config.Provider{}}
	if _, err := EnvVar(c, "nope"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("want ErrUnknownProvider, got %v", err)
	}
}

func TestCommandPresetConfigCustomUnknown(t *testing.T) {
	empty := &config.Config{Providers: map[string]config.Provider{}}

	// Preset (codex is built in).
	if got, err := Command(empty, "codex"); err != nil || got != "codex" {
		t.Fatalf("Command(codex) = %q, %v; want codex", got, err)
	}
	// Config overrides the preset.
	over := &config.Config{Providers: map[string]config.Provider{"claude": {Command: "claude-beta"}}}
	if got, _ := Command(over, "claude"); got != "claude-beta" {
		t.Fatalf("config override = %q, want claude-beta", got)
	}
	// Registered custom provider with no command yet → "" (no launcher), no error.
	custom := &config.Config{Providers: map[string]config.Provider{"foo": {EnvVar: "FOO_DIR"}}}
	if got, err := Command(custom, "foo"); err != nil || got != "" {
		t.Fatalf("Command(foo) = %q, %v; want \"\", nil", got, err)
	}
	// Entirely unknown provider → error.
	if _, err := Command(empty, "nope"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("want ErrUnknownProvider, got %v", err)
	}
}

func TestAccountDirExpandsHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	c := &config.Config{Providers: map[string]config.Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]config.Account{
			"work": {Dir: "~/.claude-work"},
		}},
	}}
	got, err := AccountDir(c, "claude", "work")
	if err != nil {
		t.Fatalf("AccountDir: %v", err)
	}
	if got != filepath.Join(home, ".claude-work") {
		t.Fatalf("AccountDir = %q, want %q", got, filepath.Join(home, ".claude-work"))
	}
}

func TestAccountDirUnknownAccount(t *testing.T) {
	c := &config.Config{Providers: map[string]config.Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]config.Account{}},
	}}
	if _, err := AccountDir(c, "claude", "ghost"); !errors.Is(err, ErrUnknownAccount) {
		t.Fatalf("want ErrUnknownAccount, got %v", err)
	}
}
