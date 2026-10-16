package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestSaveRefusesUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	src := `
[providers.claude]
env_var = "CLAUDE_CONFIG_DIR"

[providers.claude.accounts.work]
dir = "~/.claude-work"
model = "sonnet" # unknown to this build of aiacc
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = Save(path, c)
	if err == nil {
		t.Fatal("Save: want a refusal naming the unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "model") {
		t.Fatalf("refusal must name the unknown key, got: %v", err)
	}
	if !strings.Contains(err.Error(), "remove these keys from") {
		t.Fatalf("refusal must say how to resolve, got: %v", err)
	}
	// The refusal must leave the file byte-for-byte as it was.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("refused Save must not touch the file:\nbefore: %s\nafter:  %s", before, after)
	}
	// And the config stays readable, known fields intact.
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load after refusal: %v", err)
	}
	if out.Providers["claude"].Accounts["work"].Dir != "~/.claude-work" {
		t.Fatalf("known fields must survive: %+v", out.Providers["claude"].Accounts["work"])
	}
}

func TestSaveWithoutUnknownKeysSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := &Config{Providers: map[string]Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]Account{
			"work": {Dir: "~/.claude-work"},
		}},
	}}
	if err := Save(path, c); err != nil {
		t.Fatalf("Save to fresh path: %v", err)
	}
	if err := Save(path, c); err != nil {
		t.Fatalf("Save over known-keys file: %v", err)
	}
}

func TestSaveFollowsSymlinkedConfig(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.toml")
	link := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(real, []byte("[providers.claude]\nenv_var = \"CLAUDE_CONFIG_DIR\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	c, err := Load(link)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Save(link, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("Save replaced the symlink with a regular file — the link is broken")
	}
	out, err := Load(link)
	if err != nil {
		t.Fatalf("Load through link: %v", err)
	}
	if out.Providers["claude"].EnvVar != "CLAUDE_CONFIG_DIR" {
		t.Fatalf("content lost through link: %+v", out.Providers["claude"])
	}
}

func TestSaveLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	in := &Config{Providers: map[string]Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]Account{
			"work": {Dir: "~/.claude-work"},
		}},
	}}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Save(path, in); err != nil {
		t.Fatalf("re-Save (overwrite): %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.toml" {
			t.Errorf("Save left %s behind", e.Name())
		}
	}
}

func TestSavePreservesFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	in := &Config{Providers: map[string]Provider{"claude": {EnvVar: "CLAUDE_CONFIG_DIR"}}}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, in); err != nil {
		t.Fatalf("re-Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode changed by Save: got %v, want 0640", got)
	}
}

func TestUpdateConcurrentAddRemoveLosesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	seed := &Config{Providers: map[string]Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]Account{}},
	}}
	for i := 0; i < 50; i++ {
		seed.Providers["claude"].Accounts[fmt.Sprintf("r%d", i)] =
			Account{Dir: fmt.Sprintf("~/.claude-r%d", i)}
	}
	if err := Save(path, seed); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	const n = 25 // remove r0..r24, add a0..a24, concurrently
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			errs <- Update(path, func(c *Config) error {
				p := c.Providers["claude"]
				p.Accounts[fmt.Sprintf("a%d", i)] = Account{Dir: fmt.Sprintf("~/.claude-a%d", i)}
				c.Providers["claude"] = p
				return nil
			})
		}(i)
		go func(i int) {
			defer wg.Done()
			errs <- Update(path, func(c *Config) error {
				p := c.Providers["claude"]
				delete(p.Accounts, fmt.Sprintf("r%d", i))
				c.Providers["claude"] = p
				return nil
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
	}

	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	accounts := out.Providers["claude"].Accounts
	if len(accounts) != 2*n { // r25..r49 kept + a0..a24 added
		t.Fatalf("lost updates: want %d accounts, got %d: %v", 2*n, len(accounts), keys(accounts))
	}
	for i := 0; i < n; i++ {
		if _, ok := accounts[fmt.Sprintf("a%d", i)]; !ok {
			t.Fatalf("added account a%d was lost", i)
		}
		if _, ok := accounts[fmt.Sprintf("r%d", i)]; ok {
			t.Fatalf("removed account r%d came back", i)
		}
	}
}

func keys(m map[string]Account) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
