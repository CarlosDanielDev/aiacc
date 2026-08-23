package cli

import (
	"path/filepath"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
)

// linkCfg is a config with one preset provider (two accounts, in temp dirs) and
// one custom provider aiacc has no asset layout for.
func linkCfg(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	return &config.Config{Providers: map[string]config.Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude", Accounts: map[string]config.Account{
			"work":     {Dir: filepath.Join(base, "work")},
			"personal": {Dir: filepath.Join(base, "personal")},
		}},
		"acme": {EnvVar: "ACME_HOME", Command: "acme", Accounts: map[string]config.Account{
			"main": {Dir: filepath.Join(base, "acme")},
		}},
	}}
}

func TestLinkAccountsCoversPresetProvidersOnly(t *testing.T) {
	res, err := linkAccounts(linkCfg(t), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results, want the 2 claude accounts (acme has no known layout): %+v", len(res), res)
	}
	for _, r := range res {
		if r.Dir == "main" {
			t.Fatal("shared assets into a custom provider aiacc knows nothing about")
		}
	}
}

func TestLinkAccountsFiltersByArgs(t *testing.T) {
	c := linkCfg(t)
	res, err := linkAccounts(c, []string{"claude", "work"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Dir != "work" {
		t.Fatalf("filter ignored: %+v", res)
	}
	if res, err = linkAccounts(c, []string{"acme"}, false); err != nil || len(res) != 0 {
		t.Fatalf("custom provider should yield nothing: %+v %v", res, err)
	}
}
