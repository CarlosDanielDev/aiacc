package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
)

// seedConfig writes a single-provider config with one account at dir and
// returns nothing; callers pass the path from withTempConfig.
func seedConfig(t *testing.T, path, dir string, quota int) {
	t.Helper()
	cfg := &config.Config{Providers: map[string]config.Provider{
		"claude": {
			EnvVar: "CLAUDE_CONFIG_DIR",
			Accounts: map[string]config.Account{
				"work": {Dir: dir, Quota: quota},
			},
		},
	}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

func TestShellInitPrintsLaunchers(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	seedConfig(t, path, dir, 0)

	cmd := newShellInitCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"bash"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("shell-init: %v", err)
	}
	s := out.String()
	// Erases any stale pre-v0.7 hook so re-sourcing fixes the current shell.
	if !strings.Contains(s, "unset -f aiacc") {
		t.Fatalf("shell-init should erase the old aiacc hook:\n%s", s)
	}
	// One launcher named after the account ("work"), running claude with the
	// profile's dir.
	for _, want := range []string{"work() {", "CLAUDE_CONFIG_DIR=", "command claude"} {
		if !strings.Contains(s, want) {
			t.Fatalf("shell-init output missing %q:\n%s", want, s)
		}
	}
	// Cross-check with the shell package's own rendering.
	fn, err := shell.Launcher("bash", "work", "claude", "CLAUDE_CONFIG_DIR", dir)
	if err != nil {
		t.Fatalf("Launcher: %v", err)
	}
	if !strings.Contains(s, fn) {
		t.Fatalf("shell-init did not emit the expected launcher %q in:\n%s", fn, s)
	}
}

func TestRenameAccountMovesEntry(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	seedConfig(t, path, dir, 0) // provider claude, account "work"

	if err := renameAccount(path, "claude", "work", "client-x"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Providers["claude"].Accounts["work"]; ok {
		t.Fatal("old name still present")
	}
	got, ok := c.Providers["claude"].Accounts["client-x"]
	if !ok || got.Dir != dir {
		t.Fatalf("new name missing or dir lost: %+v", c.Providers["claude"].Accounts)
	}
}

func TestRenameAccountRefusesCollision(t *testing.T) {
	path := withTempConfig(t)
	c := &config.Config{Providers: map[string]config.Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]config.Account{
			"work": {Dir: t.TempDir()}, "personal": {Dir: t.TempDir()},
		}},
	}}
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	if err := renameAccount(path, "claude", "work", "personal"); err == nil {
		t.Fatal("expected error renaming onto an existing account")
	}
}

func TestHandoffCopiesSessionAndPrintsResume(t *testing.T) {
	path := withTempConfig(t)
	fromDir, toDir := t.TempDir(), t.TempDir()
	cfg := &config.Config{Providers: map[string]config.Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]config.Account{
			"work":     {Dir: fromDir},
			"personal": {Dir: toDir},
		}},
	}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	// Seed a session under the "work" account.
	sdir := filepath.Join(fromDir, "projects", "-Users-x-proj")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	sid := "abc123de-0000-0000-0000-000000000000"
	line := `{"type":"user","cwd":"/Users/x/proj","lastPrompt":"hello"}` + "\n"
	if err := os.WriteFile(filepath.Join(sdir, sid+".jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newHandoffCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"claude", "work", "personal"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	// Copied into the target account's projects, same project dir.
	if _, err := os.Stat(filepath.Join(toDir, "projects", "-Users-x-proj", sid+".jsonl")); err != nil {
		t.Fatalf("session not copied to target: %v", err)
	}
	// Resume instructions point at the recorded cwd and the target launcher.
	s := out.String()
	for _, want := range []string{"personal --resume " + sid, "cd /Users/x/proj"} {
		if !strings.Contains(s, want) {
			t.Fatalf("output missing %q:\n%s", want, s)
		}
	}
}

func TestStatusMarksActiveAccount(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	seedConfig(t, path, dir, 0)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "*") {
		t.Fatalf("status did not mark active account: %q", out.String())
	}
}

func TestUsageSumsTotals(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	logDir := filepath.Join(dir, "projects", "x")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lines := `{"message":{"usage":{"input_tokens":10,"output_tokens":5}}}
{"message":{"usage":{"input_tokens":20,"output_tokens":7}}}
`
	if err := os.WriteFile(filepath.Join(logDir, "s.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	seedConfig(t, path, dir, 0)

	cmd := newUsageCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("usage: %v", err)
	}
	// input 30, output 12, total 42.
	if !strings.Contains(out.String(), "42") {
		t.Fatalf("usage total not shown: %q", out.String())
	}
}
