package cli

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
)

const (
	resumeOldID = "aaaa1111-0000-0000-0000-000000000000"
	resumeNewID = "aaaa2222-0000-0000-0000-000000000000"
)

// resumeFixture is a temp config with one claude account "work" (with a
// settings file) holding two sessions: resumeOldID an hour old, resumeNewID
// just now. Both record cwd as their working directory.
type resumeFixture struct {
	cfgPath, dir, settings, cwd string
}

func seedResume(t *testing.T, cwd string) resumeFixture {
	t.Helper()
	f := resumeFixture{
		cfgPath:  withTempConfig(t),
		dir:      t.TempDir(),
		settings: filepath.Join(t.TempDir(), "settings.json"),
		cwd:      cwd,
	}
	cfg := &config.Config{Providers: map[string]config.Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]config.Account{
			"work": {Dir: f.dir, Settings: f.settings},
		}},
		"codex": {EnvVar: "CODEX_HOME", Command: "codex", Accounts: map[string]config.Account{
			"main": {Dir: t.TempDir()},
		}},
	}}
	if err := config.Save(f.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	sdir := filepath.Join(f.dir, "projects", "-proj")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, age := range map[string]time.Duration{resumeOldID: time.Hour, resumeNewID: 0} {
		p := filepath.Join(sdir, id+".jsonl")
		line := fmt.Sprintf(`{"type":"user","cwd":%q,"lastPrompt":"title %s"}`+"\n", cwd, id[:8])
		if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// execCall records what the resume exec seam was asked to run.
type execCall struct {
	dir  string
	args []string
	cwd  string
	id   string
}

// stubExec replaces the exec seam with a recorder, so no test ever launches
// Claude Code. The returned slice holds every call.
func stubExec(t *testing.T) *[]execCall {
	t.Helper()
	calls := &[]execCall{}
	old := execResume
	execResume = func(dir string, args []string, cwd, id string) error {
		*calls = append(*calls, execCall{dir, args, cwd, id})
		return nil
	}
	t.Cleanup(func() { execResume = old })
	return calls
}

// treeOf snapshots every path under root with its size, mode and mtime, so a
// test can prove a command wrote nothing there.
func treeOf(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s %d %v %d", p, fi.Size(), fi.Mode(), fi.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func runResume(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newResumeCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

// TestResumePrintsLatestSession: --print prints the exact command that resumes
// the most recent session in place, and runs nothing.
func TestResumePrintsLatestSession(t *testing.T) {
	f := seedResume(t, t.TempDir())
	calls := stubExec(t)
	before := treeOf(t, f.dir)

	out, errOut, err := runResume(t, "claude", "work", "--print")
	if err != nil {
		t.Fatalf("resume --print: %v", err)
	}
	want := "cd " + f.cwd + " && CLAUDE_CONFIG_DIR=" + f.dir +
		" claude --settings " + f.settings + " --resume " + resumeNewID + "\n"
	if out != want {
		t.Fatalf("printed\n%q\nwant\n%q", out, want)
	}
	if !strings.Contains(errOut, `Resuming aaaa2222 "title aaaa2222"`) || !strings.Contains(errOut, "in "+f.cwd) {
		t.Fatalf("missing the Resuming line on stderr: %q", errOut)
	}
	if len(*calls) != 0 {
		t.Fatalf("--print must not exec, got %+v", *calls)
	}
	if after := treeOf(t, f.dir); !slices.Equal(before, after) {
		t.Fatalf("resume wrote under the account dir:\nbefore %v\nafter  %v", before, after)
	}
}

// TestResumeExecsThroughSeam: without --print the command goes to the exec
// seam with the account's dir, launch args, cwd and the session id. PATH is
// empty, so a real launch could not happen even if the seam were bypassed.
func TestResumeExecsThroughSeam(t *testing.T) {
	t.Setenv("PATH", "")
	f := seedResume(t, t.TempDir())
	calls := stubExec(t)

	if _, _, err := runResume(t, "claude", "work"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	want := execCall{f.dir, []string{"--settings", f.settings}, f.cwd, resumeNewID}
	if len(*calls) != 1 || (*calls)[0].dir != want.dir || !slices.Equal((*calls)[0].args, want.args) ||
		(*calls)[0].cwd != want.cwd || (*calls)[0].id != want.id {
		t.Fatalf("exec calls = %+v, want [%+v]", *calls, want)
	}
}

// TestResumeSessionPrefix: a unique prefix resolves; an ambiguous or unknown
// one is refused with the candidates listed (short id and title).
func TestResumeSessionPrefix(t *testing.T) {
	seedResume(t, t.TempDir())
	stubExec(t)

	out, _, err := runResume(t, "claude", "work", "--print", "--session", "aaaa1")
	if err != nil || !strings.HasSuffix(out, "--resume "+resumeOldID+"\n") {
		t.Fatalf("unique prefix: out %q, err %v", out, err)
	}

	for prefix, verdict := range map[string]string{"aaaa": "is ambiguous", "zzzz": "matches no session"} {
		_, _, err := runResume(t, "claude", "work", "--print", "--session", prefix)
		if err == nil {
			t.Fatalf("prefix %q: want a refusal", prefix)
		}
		for _, want := range []string{`"` + prefix + `" ` + verdict, "aaaa1111", "title aaaa1111", "aaaa2222", "title aaaa2222"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("prefix %q: error missing %q:\n%v", prefix, want, err)
			}
		}
	}
}

// TestResumeMissingCwd: a session whose recorded cwd is gone is refused naming
// the path and the --cwd escape; --cwd <existing dir> overrides it, and a
// --cwd that does not exist is refused. Nothing is written under the account.
func TestResumeMissingCwd(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "deleted-project")
	f := seedResume(t, gone)
	calls := stubExec(t)
	before := treeOf(t, f.dir)

	_, _, err := runResume(t, "claude", "work")
	if err == nil || !strings.Contains(err.Error(), gone) || !strings.Contains(err.Error(), "--cwd") {
		t.Fatalf("missing cwd: want a refusal naming %s and --cwd, got %v", gone, err)
	}

	other := t.TempDir()
	out, _, err := runResume(t, "claude", "work", "--print", "--cwd", other)
	if err != nil || !strings.HasPrefix(out, "cd "+other+" && ") {
		t.Fatalf("--cwd override: out %q, err %v", out, err)
	}

	bad := filepath.Join(t.TempDir(), "nope")
	if _, _, err := runResume(t, "claude", "work", "--cwd", bad); err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("--cwd to a missing dir: want a refusal naming it, got %v", err)
	}

	if len(*calls) != 0 {
		t.Fatalf("a refused resume must not exec, got %+v", *calls)
	}
	if after := treeOf(t, f.dir); !slices.Equal(before, after) {
		t.Fatalf("resume wrote under the account dir:\nbefore %v\nafter  %v", before, after)
	}
}

// TestResumeRefusals: the poka-yoke messages for bad input.
func TestResumeRefusals(t *testing.T) {
	f := seedResume(t, t.TempDir())
	stubExec(t)
	empty := t.TempDir()
	cfg, err := config.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers["claude"].Accounts["empty"] = config.Account{Dir: empty}
	if err := config.Save(f.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"nope", "work"}, []string{`"nope"`, "claude", "codex"}},
		{[]string{"claude", "nope"}, []string{`"nope"`, "empty", "work"}},
		{[]string{"codex", "main"}, []string{"claude-only"}},
		{[]string{"claude", "empty"}, []string{"no sessions", empty}},
		{[]string{"claude"}, []string{"usage: aiacc resume"}},
		{[]string{"--print"}, []string{"--print needs <provider> <account>"}},
		{[]string{"--session", "aaaa"}, []string{"--session needs <provider> <account>"}},
	}
	for _, tc := range cases {
		_, _, err := runResume(t, tc.args...)
		if err == nil {
			t.Fatalf("%v: want a refusal", tc.args)
		}
		for _, w := range tc.want {
			if !strings.Contains(err.Error(), w) {
				t.Fatalf("%v: error missing %q: %v", tc.args, w, err)
			}
		}
	}
}

// TestShellWordQuotesUnsafe: the cwd comes from the transcript, so anything a
// shell would interpret is quoted in the printed command.
func TestShellWordQuotesUnsafe(t *testing.T) {
	for in, want := range map[string]string{
		"/plain/path-1.2_x": "/plain/path-1.2_x",
		"/has space":        "'/has space'",
		"/x;rm":             "'/x;rm'",
		"it's":              `'it'\''s'`,
		"":                  "''",
	} {
		if got := shellWord(in); got != want {
			t.Errorf("shellWord(%q) = %q, want %q", in, got, want)
		}
	}
}

// stubPickList answers the session list with pick (-1 = Esc) and records the
// items it was shown.
func stubPickList(t *testing.T, pick int) *[]tui.ListItem {
	t.Helper()
	shown := &[]tui.ListItem{}
	old := pickList
	pickList = func(title string, items []tui.ListItem) (int, error) {
		*shown = items
		return pick, nil
	}
	t.Cleanup(func() { pickList = old })
	return shown
}

// TestResumeTUILaunchesChosenSession: picker `u` lists the account's sessions,
// newest first, and launches the chosen one through the same exec seam.
func TestResumeTUILaunchesChosenSession(t *testing.T) {
	t.Setenv("PATH", "")
	f := seedResume(t, t.TempDir())
	calls := stubExec(t)
	shown := stubPickList(t, 1)

	if err := runResumeTUI(f.cfgPath, "claude", "work"); err != nil {
		t.Fatalf("resume TUI: %v", err)
	}
	if len(*shown) != 2 || !strings.HasPrefix((*shown)[1].Secondary, "aaaa1111") {
		t.Fatalf("session list = %+v", *shown)
	}
	if len(*calls) != 1 || (*calls)[0].id != resumeOldID || (*calls)[0].cwd != f.cwd || (*calls)[0].dir != f.dir {
		t.Fatalf("exec calls = %+v", *calls)
	}
}

// TestResumeTUIRefusals: a non-claude account, no sessions, a missing cwd, or
// Esc all launch nothing; the first three say why on screen.
func TestResumeTUIRefusals(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "deleted-project")
	f := seedResume(t, gone)
	calls := stubExec(t)
	stubPickList(t, 0)
	empty := t.TempDir()
	cfg, err := config.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers["claude"].Accounts["empty"] = config.Account{Dir: empty}
	if err := config.Save(f.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ provider, account, want string }{
		{"codex", "main", "claude-only"},
		{"claude", "empty", empty},
		{"claude", "work", gone},
	} {
		captured := captureHandoffMessage(t)
		if err := runResumeTUI(f.cfgPath, tc.provider, tc.account); err != nil {
			t.Fatalf("%s/%s: want an on-screen message, got error %v", tc.provider, tc.account, err)
		}
		if len(*captured) == 0 || !strings.Contains((*captured)[0].Text, tc.want) {
			t.Fatalf("%s/%s: message %+v does not mention %q", tc.provider, tc.account, *captured, tc.want)
		}
	}

	stubPickList(t, -1)
	if err := runResumeTUI(f.cfgPath, "claude", "work"); err != nil {
		t.Fatalf("Esc: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("refused or cancelled resumes must not exec, got %+v", *calls)
	}
}

// TestPickerResumeKeyRoutesToResume drives runPicker: `u` on a row resumes a
// session of that row's account, and a refusal returns to the list. The
// launcher screen answers `u` on the given row, then quits.
func TestPickerResumeKeyRoutesToResume(t *testing.T) {
	t.Setenv("PATH", "")
	f := seedResume(t, t.TempDir())
	calls := stubExec(t)
	stubPickList(t, 0)

	drive := func(account string) {
		t.Helper()
		answers := 0
		old := runLauncher
		runLauncher = func(rows []tui.Row, _ bool, _ string) (tui.Result, error) {
			answers++
			if answers > 1 {
				return tui.Result{Kind: tui.Cancelled}, nil
			}
			for i, r := range rows {
				if r.Account == account {
					return tui.Result{Kind: tui.Resume, Index: i}, nil
				}
			}
			t.Fatalf("no row for %s in %+v", account, rows)
			return tui.Result{}, nil
		}
		t.Cleanup(func() { runLauncher = old })
		if err := runPicker(""); err != nil {
			t.Fatalf("picker: %v", err)
		}
		if answers != 2 {
			t.Fatalf("picker did not return to the list after resume: %d screens", answers)
		}
	}

	captured := captureHandoffMessage(t)
	drive("main") // codex row: refused on screen, nothing launched
	if len(*calls) != 0 || len(*captured) == 0 || !strings.Contains((*captured)[0].Text, "claude-only") {
		t.Fatalf("codex row: calls %+v, message %+v", *calls, *captured)
	}

	drive("work")
	if len(*calls) != 1 || (*calls)[0].dir != f.dir || (*calls)[0].id != resumeNewID {
		t.Fatalf("work row: exec calls = %+v", *calls)
	}
}

// TestResumeUnreadableTranscript: a transcript that cannot be read is refused
// naming its path and the read error, on the CLI and on picker select, not
// misreported as a session with no recorded directory.
func TestResumeUnreadableTranscript(t *testing.T) {
	f := seedResume(t, t.TempDir())
	calls := stubExec(t)
	stubPickList(t, 0)
	newest := filepath.Join(f.dir, "projects", "-proj", resumeNewID+".jsonl")
	if err := os.Chmod(newest, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(newest, 0o600) })
	if _, err := os.ReadFile(newest); err == nil {
		t.Skip("running as a user that reads mode-0 files (root?)")
	}

	_, _, err := runResume(t, "claude", "work", "--print")
	if err == nil || !strings.Contains(err.Error(), newest) || !strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "--cwd") {
		t.Fatalf("CLI: want a refusal naming %s and the read error, without the --cwd hint; got %v", newest, err)
	}

	captured := captureHandoffMessage(t)
	if err := runResumeTUI(f.cfgPath, "claude", "work"); err != nil {
		t.Fatalf("picker: want an on-screen message, got error %v", err)
	}
	if len(*captured) == 0 || !strings.Contains((*captured)[0].Text, newest) || !strings.Contains((*captured)[0].Text, "permission denied") {
		t.Fatalf("picker: message %+v does not name %s and the read error", *captured, newest)
	}
	if len(*calls) != 0 {
		t.Fatalf("an unreadable transcript must not exec, got %+v", *calls)
	}
}

// TestFindSessionKeepsListingErrors: when session.Find fails to list the
// account's projects (here: unreadable), that error comes back unchanged, not
// as "matches no session" with a stale candidate list.
func TestFindSessionKeepsListingErrors(t *testing.T) {
	f := seedResume(t, t.TempDir())
	sessions, _, _, err := sessionChoices(f.dir)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("seed: %d sessions, err %v", len(sessions), err)
	}
	projects := filepath.Join(f.dir, "projects")
	if err := os.Chmod(projects, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(projects, 0o755) })
	if _, err := os.ReadDir(projects); err == nil {
		t.Skip("running as a user that reads mode-0 dirs (root?)")
	}

	_, err = findSession(f.dir, sessions, "zzzz")
	if err == nil || !strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "matches no session") {
		t.Fatalf("want the listing error unchanged, got %v", err)
	}
}
