package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/session"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
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

func TestSaveAccountFillsPresetAndCustomProvider(t *testing.T) {
	path := withTempConfig(t)

	// Codex is a built-in preset → env var and command filled automatically.
	if err := saveAccount(path, "codex", "work", "/x", 0, "", "", ""); err != nil {
		t.Fatal(err)
	}
	// A custom provider gets its env var + command from the flags.
	if err := saveAccount(path, "glab", "me", "/y", 0, "GLAB_CONFIG_DIR", "glab", ""); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p := c.Providers["codex"]; p.EnvVar != "CODEX_HOME" || p.Command != "codex" {
		t.Fatalf("codex preset not applied: %+v", p)
	}
	if p := c.Providers["glab"]; p.EnvVar != "GLAB_CONFIG_DIR" || p.Command != "glab" {
		t.Fatalf("custom provider not stored: %+v", p)
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
	// Resume instructions point at the recorded cwd (quoted — the line is
	// meant to be pasted into a shell) and the target launcher.
	s := out.String()
	for _, want := range []string{"personal --resume " + sid, `cd '/Users/x/proj'`} {
		if !strings.Contains(s, want) {
			t.Fatalf("output missing %q:\n%s", want, s)
		}
	}
}

// TestHandoffQuotesResumePath: the resume command is printed for the user to
// paste into a shell, so a recorded cwd with spaces or a quote cannot break
// out of the cd — it is single-quote escaped, POSIX-style.
func TestHandoffQuotesResumePath(t *testing.T) {
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
	sdir := filepath.Join(fromDir, "projects", "-Users-x-proj")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	sid := "abc123de-0000-0000-0000-000000000000"
	cwd := `/Users/x/my proj'; echo pwned`
	line := `{"type":"user","cwd":"` + cwd + `","lastPrompt":"hello"}` + "\n"
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
	want := `cd '/Users/x/my proj'\''; echo pwned'`
	if !strings.Contains(out.String(), want) {
		t.Fatalf("output missing %q:\n%s", want, out.String())
	}
}

// TestHandoffDoneScreenQuotesResumePath: the "hand off · done" screen is the
// interactive twin of handoffDirect's printed resume command, and its Blue
// lines are exactly what ctrl-C copies to the clipboard — so the recorded cwd
// must be single-quote escaped there too.
func TestHandoffDoneScreenQuotesResumePath(t *testing.T) {
	meta := session.Meta{Cwd: `/Users/x/my proj'; echo pwned`, Title: "t"}
	lines := handoffDoneLines("personal", meta, "abc123")
	var cds []string
	for _, l := range lines {
		if l.Color == tui.Blue && strings.HasPrefix(l.Text, "cd ") {
			cds = append(cds, l.Text)
		}
	}
	want := `cd '/Users/x/my proj'\''; echo pwned'`
	if len(cds) != 1 || cds[0] != want {
		t.Fatalf("done screen cd line = %q, want %q", cds, want)
	}
}

// TestHandoffDirectRejectsNonClaude pins the claude-only boundary: hand off
// copies Claude Code transcripts, so a codex pair is refused, not silently
// copied into a directory claude will never read.
func TestHandoffDirectRejectsNonClaude(t *testing.T) {
	path := withTempConfig(t)
	cfg := &config.Config{Providers: map[string]config.Provider{
		"codex": {EnvVar: "CODEX_HOME", Command: "codex", Accounts: map[string]config.Account{
			"work":     {Dir: t.TempDir()},
			"personal": {Dir: t.TempDir()},
		}},
	}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	cmd := newHandoffCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"codex", "work", "personal"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected handoff between codex accounts to be refused")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Fatalf("error should name the claude-only boundary, got: %v", err)
	}
}

// TestHandoffDirectRejectsClaudeCommandWithOtherEnv: launching claude is not
// enough — the resume exec hardcodes CLAUDE_CONFIG_DIR, so a provider with a
// foreign env var would resume into the wrong directory. The gate refuses it.
func TestHandoffDirectRejectsClaudeCommandWithOtherEnv(t *testing.T) {
	path := withTempConfig(t)
	cfg := &config.Config{Providers: map[string]config.Provider{
		"clonede": {EnvVar: "OTHER_DIR", Command: "claude", Accounts: map[string]config.Account{
			"work":     {Dir: t.TempDir()},
			"personal": {Dir: t.TempDir()},
		}},
	}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	cmd := newHandoffCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"clonede", "work", "personal"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected handoff on a claude-command provider with a foreign env var to be refused")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Fatalf("error should name the claude-only boundary, got: %v", err)
	}
}

// TestHandoffTUIRefusesNonClaudeRow drives the picker's handoff action on a
// codex row and pins the in-screen claude-only refusal — the gate must fire
// before any session listing, so the flow never starts for a non-claude row.
func TestHandoffTUIRefusesNonClaudeRow(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	cfg := &config.Config{Providers: map[string]config.Provider{
		"codex": {EnvVar: "CODEX_HOME", Command: "codex", Accounts: map[string]config.Account{
			"main": {Dir: dir},
		}},
	}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	// A session in claude layout: if the gate is gone, the flow proceeds to the
	// session list instead of refusing, and this test fails.
	sdir := filepath.Join(dir, "projects", "-Users-x-proj")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdir, "abc.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	captured := captureHandoffMessage(t)
	if err := runHandoffTUI(path, "codex", "main"); err != nil {
		t.Fatalf("handoff should show the refusal in-screen, got error: %v", err)
	}
	if len(*captured) == 0 || !strings.Contains((*captured)[0].Text, "claude-only") {
		t.Fatalf("expected the claude-only message, got %+v", *captured)
	}
}

// TestHandoffPickSourceSaysClaudeOnlyWithoutClaudeAccounts drives the bare
// interactive handoff with no claude accounts configured and pins the
// claude-only message (not the generic "need two accounts").
func TestHandoffPickSourceSaysClaudeOnlyWithoutClaudeAccounts(t *testing.T) {
	path := withTempConfig(t)
	cfg := &config.Config{Providers: map[string]config.Provider{
		"codex": {EnvVar: "CODEX_HOME", Command: "codex", Accounts: map[string]config.Account{
			"main": {Dir: t.TempDir()},
		}},
	}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	captured := captureHandoffMessage(t)
	if err := handoffPickSource(path); err != nil {
		t.Fatalf("handoff should show the refusal in-screen, got error: %v", err)
	}
	if len(*captured) == 0 || !strings.Contains((*captured)[0].Text, "claude-only") {
		t.Fatalf("expected the claude-only message, got %+v", *captured)
	}
}

// captureHandoffMessage swaps showMessage for a recorder and restores it on
// cleanup, so a test can drive a handoff entry point and read the message it
// would have shown, without a tty.
func captureHandoffMessage(t *testing.T) *[]tui.Line {
	t.Helper()
	captured := &[]tui.Line{}
	old := showMessage
	showMessage = func(title string, body []tui.Line) error {
		*captured = body
		return nil
	}
	t.Cleanup(func() { showMessage = old })
	return captured
}

// TestClaudeAccountsListsClaudeOnly pins the interactive handoff list to the
// accounts whose launcher is Claude Code — a codex row must not start a flow
// that ends in claude.
func TestClaudeAccountsListsClaudeOnly(t *testing.T) {
	c := &config.Config{Providers: map[string]config.Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude", Accounts: map[string]config.Account{
			"work":     {Dir: "/w"},
			"personal": {Dir: "/p"},
		}},
		"codex": {EnvVar: "CODEX_HOME", Command: "codex", Accounts: map[string]config.Account{
			"main": {Dir: "/c"},
		}},
	}}
	var got []string
	for _, a := range claudeAccounts(c) {
		got = append(got, a.provider+"/"+a.account)
	}
	want := []string{"claude/personal", "claude/work"}
	if !slices.Equal(got, want) {
		t.Fatalf("claudeAccounts = %v, want %v", got, want)
	}
}

// TestLaunchesClaude pins the predicate every handoff entry point gates on.
func TestLaunchesClaude(t *testing.T) {
	c := &config.Config{Providers: map[string]config.Provider{
		"claude":   {EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"},
		"clone":    {EnvVar: "OTHER_DIR", Command: "claude"}, // launches claude, isolates it differently
		"codex":    {EnvVar: "CODEX_HOME", Command: "codex"},
		"nolaunch": {EnvVar: "X_DIR", Command: ""},
	}}
	for _, tt := range []struct {
		provider string
		want     bool
	}{
		{"claude", true},
		{"clone", false},
		{"codex", false},
		{"nolaunch", false},
		{"unknown", false},
	} {
		if got := launchesClaude(c, tt.provider); got != tt.want {
			t.Errorf("launchesClaude(%q) = %v, want %v", tt.provider, got, tt.want)
		}
	}
}

// TestResumeCmdBuildsArgvAndEnv pins the argv/env assembly factored out of the
// exec: the account's launch args come ahead of --resume, and the target dir
// overrides the config-dir env var without dropping the rest of the environment.
func TestResumeCmdBuildsArgvAndEnv(t *testing.T) {
	environ := []string{"HOME=/home/x", "CLAUDE_CONFIG_DIR=/old", "PATH=/bin"}
	argv, env := resumeCmd([]string{"--settings", "/w/settings.json"}, "/acct/dir", "abc-123", environ)
	wantArgv := []string{"claude", "--settings", "/w/settings.json", "--resume", "abc-123"}
	if !slices.Equal(argv, wantArgv) {
		t.Fatalf("argv = %v, want %v", argv, wantArgv)
	}
	if !slices.Contains(env, "CLAUDE_CONFIG_DIR=/acct/dir") {
		t.Fatalf("env missing the config-dir override: %v", env)
	}
	if !slices.Contains(env, "HOME=/home/x") {
		t.Fatalf("env dropped unrelated vars: %v", env)
	}
	if slices.Contains(env, "CLAUDE_CONFIG_DIR=/old") {
		t.Fatalf("env kept the stale config dir: %v", env)
	}

	argv, _ = resumeCmd(nil, "/acct/dir", "abc-123", nil)
	if want := []string{"claude", "--resume", "abc-123"}; !slices.Equal(argv, want) {
		t.Fatalf("argv without launch args = %v, want %v", argv, want)
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

func TestSaveAccountSettings(t *testing.T) {
	path := withTempConfig(t)
	if err := saveAccount(path, "claude", "glm", "/g", 0, "", "", "~/glm.json"); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers["claude"].Accounts["glm"].Settings; got != "~/glm.json" {
		t.Fatalf("settings path not stored as given: %q", got)
	}
	// codex has no settings flag; storing a path it would never load is refused.
	if err := saveAccount(path, "codex", "x", "/x", 0, "", "", "/s.json"); err == nil {
		t.Fatal("want an error for --settings on a provider without a settings flag")
	}
}
