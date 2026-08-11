package config

import (
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	in := &Config{Providers: map[string]Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]Account{
			"work":     {Dir: "~/.claude-work", Quota: 0},
			"personal": {Dir: "~/.claude-personal", Quota: 100},
		}},
	}}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Providers["claude"].EnvVar != "CLAUDE_CONFIG_DIR" {
		t.Fatalf("env_var lost: %+v", out.Providers["claude"])
	}
	if out.Providers["claude"].Accounts["personal"].Quota != 100 {
		t.Fatalf("quota lost: %+v", out.Providers["claude"].Accounts["personal"])
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	out, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if out.Providers == nil || len(out.Providers) != 0 {
		t.Fatalf("want empty non-nil providers, got %+v", out.Providers)
	}
}
