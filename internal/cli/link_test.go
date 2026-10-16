package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/share"
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

// From the second replace round on, the newest original sits at
// .aiacc-bak2 or later — a report that only ever says ".aiacc-bak" sends the
// user recovering after the wrong generation. Both report surfaces must print
// the path that was actually chosen.
func TestLinkReportsNameTheBackupPath(t *testing.T) {
	bak := filepath.Join(t.TempDir(), "CLAUDE.md.aiacc-bak2")
	results := []share.Result{{
		Dir: "work",
		Entries: []share.Entry{
			{Name: "CLAUDE.md", Status: share.Linked, Backup: bak},
			{Name: "skills", Status: share.Linked},
		},
	}}

	var buf bytes.Buffer
	printLinkReport(&buf, results, true)
	if out := buf.String(); !strings.Contains(out, bak) {
		t.Fatalf("plain report hides the backup path: %q", out)
	}

	var joined strings.Builder
	for _, l := range linkLines(results, true) {
		joined.WriteString(l.Text + "\n")
	}
	if out := joined.String(); !strings.Contains(out, bak) {
		t.Fatalf("terminal report hides the backup path: %q", out)
	}
}
