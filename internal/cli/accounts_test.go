package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/usage"
	"github.com/spf13/cobra"
)

func withTempConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	old := configPath
	configPath = func() (string, error) { return p, nil }
	t.Cleanup(func() { configPath = old })
	return p
}

func TestAddThenListShowsAccount(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "work", "--dir", dir})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}

	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Providers["claude"].Accounts["work"].Dir != dir {
		t.Fatalf("account not saved: %+v", c.Providers["claude"])
	}

	list := newListCmd()
	var out bytes.Buffer
	list.SetOut(&out)
	if err := list.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "work") || !strings.Contains(out.String(), "claude") {
		t.Fatalf("list output missing account: %q", out.String())
	}
}

// TestAddRefusesUnsafeName: the account name is also the launcher filename
// (`binDir/<name>`), so a traversal or junk name must be refused at
// registration with the rule named — before it can reach setup, remove,
// doctor or shell-init. Nothing is registered.
func TestAddRefusesUnsafeName(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "../x", "--dir", dir})
	err := add.Execute()
	if err == nil {
		t.Fatalf(`add claude '../x' was accepted`)
	}
	for _, want := range []string{"../x", "letters, digits"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not mention %q: %v", want, err)
		}
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Providers["claude"].Accounts) != 0 {
		t.Fatalf("account registered despite the refusal: %+v", c.Providers["claude"].Accounts)
	}
}

// TestRenameRefusesUnsafeName: a rename is a second way in for the same
// traversal (the new name becomes the launcher filename), so a junk name is
// refused with the rule named and the old registration survives. Driven
// through the cobra command, so the guard holds wherever it lives between
// the command and renameAccount.
func TestRenameRefusesUnsafeName(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	seedConfig(t, path, dir, 0) // provider claude, account "work"

	rename := newRenameCmd()
	rename.SetArgs([]string{"claude", "work", "bad name"})
	err := rename.Execute()
	if err == nil {
		t.Fatalf("rename to a junk name was accepted")
	}
	if !strings.Contains(err.Error(), "letters, digits") {
		t.Fatalf("error does not name the rule: %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Providers["claude"].Accounts["work"]; !ok {
		t.Fatal("the old registration was lost despite the refusal")
	}
}

func TestRemoveDeletesAccount(t *testing.T) {
	withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "work", "--dir", dir})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}
	rm := newRemoveCmd()
	rm.SetArgs([]string{"claude", "work"})
	if err := rm.Execute(); err != nil {
		t.Fatalf("remove: %v", err)
	}

	path, _ := configPath()
	c, _ := config.Load(path)
	if _, ok := c.Providers["claude"].Accounts["work"]; ok {
		t.Fatalf("account still present after remove")
	}
}

// TestAddSettingsKeyword: --settings glm is an endpoint keyword, not a path —
// the skeleton is generated into the account dir (mode 0600) and the account
// registers with the generated path.
func TestAddSettingsKeyword(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "foo", "--dir", dir, "--settings", "glm"})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}

	skeleton := filepath.Join(dir, "glm.json")
	info, err := os.Stat(skeleton)
	if err != nil {
		t.Fatalf("skeleton not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("skeleton mode %v, want 0600", info.Mode().Perm())
	}
	body, err := os.ReadFile(skeleton)
	if err != nil {
		t.Fatalf("read skeleton: %v", err)
	}
	for _, want := range []string{"ANTHROPIC_BASE_URL", provider.Presets["claude"].Endpoints["glm"].BaseURL, provider.PlaceholderToken} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("skeleton missing %s:\n%s", want, body)
		}
	}

	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.Providers["claude"].Accounts["foo"].Settings; got != skeleton {
		t.Fatalf("settings = %q, want the generated path %q", got, skeleton)
	}
}

// TestAddSettingsPathUnchanged: a real path behaves exactly as before (no
// generation, no write), and a keyword whose target file already exists
// aborts the add with the path named, registering nothing.
func TestAddSettingsPathUnchanged(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	real := filepath.Join(dir, "my-endpoint.json")
	want := []byte("{\"env\":{}}\n")
	if err := os.WriteFile(real, want, 0o600); err != nil {
		t.Fatalf("write real settings: %v", err)
	}

	add := newAddCmd()
	add.SetArgs([]string{"claude", "foo", "--dir", dir, "--settings", real})
	if err := add.Execute(); err != nil {
		t.Fatalf("add with a real path: %v", err)
	}
	body, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("read real settings: %v", err)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("real settings file was rewritten:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "glm.json")); err == nil {
		t.Fatalf("a skeleton was generated for a path value")
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.Providers["claude"].Accounts["foo"].Settings; got != real {
		t.Fatalf("settings = %q, want the path verbatim %q", got, real)
	}

	target := filepath.Join(dir, "glm.json")
	if err := os.WriteFile(target, []byte("the user's own file\n"), 0o600); err != nil {
		t.Fatalf("write existing target: %v", err)
	}
	add.SetArgs([]string{"claude", "bar", "--dir", dir, "--settings", "glm"})
	err = add.Execute()
	if err == nil {
		t.Fatalf("add onto an existing glm.json succeeded")
	}
	if !strings.Contains(err.Error(), target) {
		t.Fatalf("error does not name the existing path %s: %v", target, err)
	}
	body, err = os.ReadFile(target)
	if err != nil || string(body) != "the user's own file\n" {
		t.Fatalf("existing glm.json was touched: %q, %v", body, err)
	}
	c, err = config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := c.Providers["claude"].Accounts["bar"]; ok {
		t.Fatalf("account registered despite the aborted add")
	}
}

// TestAddSettingsValueShape pins the path-vs-keyword boundary of the
// --settings value: a dot or a ~ reads as a path and registers verbatim with
// nothing generated, a bare word reads as a keyword attempt and — the
// registry being case-sensitive — an unknown one is refused with the valid
// keywords listed.
func TestAddSettingsValueShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		value    string
		wantPath string // registered verbatim, when set
		wantErr  string // refused with this substring, when set
	}{
		{name: "dot reads as a relative path", value: "settings.json", wantPath: "settings.json"},
		{name: "tilde reads as a home path", value: "~/e.json", wantPath: "~/e.json"},
		{name: "uppercase is an unknown keyword", value: "GLM", wantErr: "custom, glm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := withTempConfig(t)
			dir := t.TempDir()

			add := newAddCmd()
			add.SetArgs([]string{"claude", "foo", "--dir", dir, "--settings", tc.value})
			err := add.Execute()
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("--settings %s was accepted", tc.value)
				}
				for _, want := range []string{tc.value, tc.wantErr} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("error does not mention %q: %v", want, err)
					}
				}
			} else if err != nil {
				t.Fatalf("add: %v", err)
			}

			if tc.wantErr != "" {
				// A refusal lands before the create, so the dir is untouched
				// (a success row is populated by linkProfile's symlinks).
				if entries, err := os.ReadDir(dir); err != nil {
					t.Fatalf("read dir: %v", err)
				} else if len(entries) != 0 {
					t.Fatalf("the account dir was touched by the refusal: %v", entries)
				}
			} else if _, err := os.Stat(filepath.Join(dir, "glm.json")); err == nil {
				t.Fatalf("a skeleton was generated for the path value %q", tc.value)
			}
			c, err := config.Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			accounts := c.Providers["claude"].Accounts
			if tc.wantErr != "" {
				if len(accounts) != 0 {
					t.Fatalf("account registered despite the refusal: %+v", accounts)
				}
				return
			}
			if got := accounts["foo"].Settings; got != tc.wantPath {
				t.Fatalf("settings = %q, want %q verbatim", got, tc.wantPath)
			}
		})
	}
}

// TestAddSettingsKeywordExpandsTildeDir: a literal --dir ~ must not grow a
// "./~" directory — no shared-asset links, no skeleton, nothing lands there;
// the skeleton is written at the expanded location while the account
// registers the path as typed, matching the add screen.
func TestAddSettingsKeywordExpandsTildeDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	work := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(oldWd) })

	path := withTempConfig(t)
	add := newAddCmd()
	add.SetArgs([]string{"claude", "foo", "--dir", "~/.claude-foo", "--settings", "glm"})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}

	expanded := filepath.Join(tmpHome, ".claude-foo", "glm.json")
	if _, err := os.Stat(expanded); err != nil {
		t.Fatalf("skeleton not at the expanded location %s: %v", expanded, err)
	}
	if _, err := os.Stat(filepath.Join(work, "~")); err == nil {
		t.Fatalf("a literal ~ directory grew in the working directory")
	}
	literal := filepath.Join(work, "~", ".claude-foo", "glm.json")
	if _, err := os.Stat(literal); err == nil {
		t.Fatalf("skeleton written under a literal ~ directory: %s", literal)
	}

	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := filepath.Join("~/.claude-foo", "glm.json")
	if got := c.Providers["claude"].Accounts["foo"].Settings; got != want {
		t.Fatalf("settings = %q, want the dir as typed: %q", got, want)
	}
}

// TestAddFlagsWithNoArgsPrefillWizard: flags combined with no arguments reach
// the piped wizard as prefills — each flag preempts its question instead of
// being silently dropped, so the piped answers cover only what remains.
func TestAddFlagsWithNoArgsPrefillWizard(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	oldTerm := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = oldTerm })

	add := newAddCmd()
	add.SetIn(bytes.NewBufferString("work\n")) // dir and settings come from the flags
	add.SetArgs([]string{"--dir", dir, "--settings", "glm", "--quota", "7"})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	acct := c.Providers["claude"].Accounts["work"]
	if acct.Dir != dir || acct.Quota != 7 {
		t.Fatalf("flags not applied: %+v", acct)
	}
	if got := acct.Settings; got != filepath.Join(dir, "glm.json") {
		t.Fatalf("settings = %q, want the generated path %q", got, filepath.Join(dir, "glm.json"))
	}
}

// TestAddFlagsInteractiveNoArgsError: on a terminal the flags have no form
// field to prefill, so they are refused with the two spellings named instead
// of being silently ignored.
func TestAddFlagsInteractiveNoArgsError(t *testing.T) {
	path := withTempConfig(t)
	oldTerm := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = oldTerm })

	add := newAddCmd()
	add.SetArgs([]string{"--dir", t.TempDir()})
	err := add.Execute()
	if err == nil {
		t.Fatal("flags with no arguments were silently dropped on a terminal")
	}
	for _, want := range []string{"--dir", "provider", "account"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not mention %q: %v", want, err)
		}
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Providers) != 0 {
		t.Fatalf("something was registered despite the refusal: %+v", c.Providers)
	}
}

// TestAddSettingsUnknown: a bare word that is no endpoint keyword is refused
// with the valid ones listed (a typo must not silently register a bogus
// settings path), and custom without its endpoint flags is refused naming them.
func TestAddSettingsUnknown(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "foo", "--dir", dir, "--settings", "kimi"})
	err := add.Execute()
	if err == nil {
		t.Fatalf("--settings kimi was accepted")
	}
	for _, want := range []string{"kimi", "glm", "custom"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("kimi error does not list valid keywords (missing %q): %v", want, err)
		}
	}

	add.SetArgs([]string{"claude", "bar", "--dir", dir, "--settings", "custom"})
	err = add.Execute()
	if err == nil {
		t.Fatalf("--settings custom was accepted")
	}
	if !strings.Contains(err.Error(), "--base-url") || !strings.Contains(err.Error(), "--model") {
		t.Fatalf("custom error does not name the flags to add: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "custom.json")); err == nil {
		t.Fatalf("a custom skeleton was generated without its endpoint flags")
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(c.Providers["claude"].Accounts) != 0 {
		t.Fatalf("accounts registered despite the refusals: %+v", c.Providers["claude"].Accounts)
	}
}

// A refused name must leave nothing on disk, whichever check refuses it: a name
// another provider owns, or a name that is not a valid launcher filename. The
// dir the user typed must never be created first (the issue's own scenario is
// `aiacc add codex work --dir ~/.codex-work`).
func TestAddRefusedNameCreatesNoDir(t *testing.T) {
	cases := []struct {
		name, provider, account string
	}{
		{"taken by another provider", "codex", "work"},
		{"invalid launcher name", "claude", "../evil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := withTempConfig(t)
			seedConfig(t, path, t.TempDir(), 0) // claude "work"
			dir := filepath.Join(t.TempDir(), "acct")

			add := newAddCmd()
			add.SetArgs([]string{tc.provider, tc.account, "--dir", dir})
			if err := add.Execute(); err == nil {
				t.Fatalf("%s %s was accepted", tc.provider, tc.account)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("refused add still created %s (stat err: %v)", dir, err)
			}
		})
	}
}

// The message must match the case: a same-provider re-add is not a cross-provider
// collision, and both need a next step the user can take.
func TestAddTakenNameMessageNamesTheCase(t *testing.T) {
	path := withTempConfig(t)
	seedConfig(t, path, t.TempDir(), 0) // claude "work"

	err := saveAccount(path, "claude", "work", t.TempDir(), 0, "", "", "")
	if err == nil {
		t.Fatal("same-provider re-add was accepted")
	}
	for _, want := range []string{"already exists under claude", "pick another name", "aiacc remove claude work"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("same-provider message lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "cannot use it too") {
		t.Errorf("same-provider re-add is not a cross-provider collision: %v", err)
	}

	err = saveAccount(path, "codex", "work", t.TempDir(), 0, "", "", "")
	if err == nil {
		t.Fatal("cross-provider add was accepted")
	}
	for _, want := range []string{"already exists under claude", "codex cannot use it too", "aiacc remove claude work"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("cross-provider message lacks %q: %v", want, err)
		}
	}
}

// A directory is never registered as a settings file: add refuses it and
// writes no account, so the launcher the profile would get is never generated.
func TestAddSettingsRefusesDirectory(t *testing.T) {
	path := withTempConfig(t)
	dir := filepath.Join(t.TempDir(), "acct") // does not exist yet: a refused add must not create it
	settingsDir := filepath.Join(t.TempDir(), "claude-glm")
	if err := os.Mkdir(settingsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	add := newAddCmd()
	add.SetArgs([]string{"claude", "foo", "--dir", dir, "--settings", settingsDir})
	err := add.Execute()
	if err == nil || !strings.Contains(err.Error(), "settings path is a directory") {
		t.Fatalf("add with a directory: got %v, want a refusal naming the directory", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("a refused add created the account dir %s", dir)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("a refused add wrote the config file %s", path)
	}
	if c, err := config.Load(path); err != nil {
		t.Fatalf("load: %v", err)
	} else if _, ok := c.Providers["claude"].Accounts["foo"]; ok {
		t.Fatalf("a refused add still registered the account")
	}
}

// A snapshot taken before another process registers a name passes the check, so
// an interactive caller must re-read the config: checkNameOnDisk refuses the name
// the snapshot would have let through.
func TestCheckNameOnDiskSeesRegistrationAfterSnapshot(t *testing.T) {
	path := withTempConfig(t)
	snapshot, err := config.Load(path) // empty: taken before the other process writes
	if err != nil {
		t.Fatal(err)
	}
	seedConfig(t, path, t.TempDir(), 0) // claude "work", registered meanwhile

	if err := checkNewName(snapshot, "codex", "work"); err != nil {
		t.Fatalf("the stale snapshot should pass (that is the race): %v", err)
	}
	if err := checkNameOnDisk(path, "codex", "work"); err == nil {
		t.Fatal("checkNameOnDisk accepted a name registered on disk")
	}
}

// runList executes list with args and returns its stdout.
func runList(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newListCmd()
	cmd.SetArgs(append([]string{}, args...))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list %v: %v", args, err)
	}
	return out.String()
}

// seedClaudeLogs writes two assistant turns (input 30, output 12) under dir.
func seedClaudeLogs(t *testing.T, dir string) {
	t.Helper()
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
}

// TestListUsageFlagShowsUsageTotals: `list --usage` adds the same totals and
// USED/QUOTA cells `usage` prints for the account.
func TestListUsageFlagShowsUsageTotals(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	seedClaudeLogs(t, dir)
	seedConfig(t, path, dir, 100)

	header, row := splitRows(t, runList(t, "--usage"), "claude", "work")
	if want := []string{"INPUT", "OUTPUT", "TOTAL", "USED/QUOTA"}; !slices.Equal(header[len(header)-4:], want) {
		t.Fatalf("list --usage header = %v, want suffix %v", header, want)
	}
	if want := []string{"30", "12", "42", "42/100"}; !slices.Equal(row[len(row)-4:], want) {
		t.Fatalf("list --usage cells = %v, want suffix %v", row, want)
	}
}

// TestListStatusFlagMarksActiveAccount: `list --status` adds the ACTIVE marker
// and LAST-USED that `status` prints for the account.
func TestListStatusFlagMarksActiveAccount(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	seedConfig(t, path, dir, 0)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	header, row := splitRows(t, runList(t, "--status"), "claude", "work")
	if want := []string{"ACTIVE", "LAST-USED"}; !slices.Equal(header[len(header)-2:], want) {
		t.Fatalf("list --status header = %v, want suffix %v", header, want)
	}
	if want := "*"; !slices.Contains(row, want) {
		t.Fatalf("list --status did not mark the active account: %v", row)
	}
}

// TestListDefaultSkipsUsageWalk: the session-log walk is opt-in. A plain
// `list` must not call usage.Aggregate, and its header stays LOGIN/DIR only.
func TestListDefaultSkipsUsageWalk(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	seedClaudeLogs(t, dir)
	seedConfig(t, path, dir, 100)

	calls := 0
	old := aggregateUsage
	aggregateUsage = func(d string) (usage.Totals, error) { calls++; return old(d) }
	t.Cleanup(func() { aggregateUsage = old })

	out := runList(t)
	if calls != 0 {
		t.Fatalf("default list walked session logs %d times", calls)
	}
	if got := strings.Fields(strings.SplitN(out, "\n", 2)[0]); !slices.Equal(got, []string{"PROVIDER", "ACCOUNT", "LOGIN", "DIR"}) {
		t.Fatalf("default list header = %v", got)
	}
}

// TestStatusAndUsageAliasesKeepOutput pins the hidden aliases' stdout to the
// bytes the commands printed before they shared list's code. LAST-USED is
// fixed by setting the dir's mtime.
func TestStatusAndUsageAliasesKeepOutput(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	seedClaudeLogs(t, dir)
	seedConfig(t, path, dir, 100)
	mtime := time.Date(2026, 1, 2, 3, 4, 0, 0, time.Local)
	if err := os.Chtimes(dir, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	run := func(cmd *cobra.Command, args ...string) string {
		t.Helper()
		cmd.SetArgs(append([]string{}, args...))
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s %v: %v", cmd.Name(), args, err)
		}
		return out.String()
	}
	const wantStatus = "PROVIDER  ACCOUNT  ACTIVE  LAST-USED\n" +
		"claude    work     *       2026-01-02 03:04\n"
	const wantUsage = "PROVIDER  ACCOUNT  INPUT  OUTPUT  TOTAL  USED/QUOTA\n" +
		"claude    work     30     12      42     42/100\n"
	if got := run(newStatusCmd()); got != wantStatus {
		t.Fatalf("status output changed:\n%q\nwant\n%q", got, wantStatus)
	}
	if got := run(newUsageCmd()); got != wantUsage {
		t.Fatalf("usage output changed:\n%q\nwant\n%q", got, wantUsage)
	}
	if got := run(newUsageCmd(), "claude"); got != wantUsage {
		t.Fatalf("usage claude output changed:\n%q\nwant\n%q", got, wantUsage)
	}
	const wantEmpty = "PROVIDER  ACCOUNT  INPUT  OUTPUT  TOTAL  USED/QUOTA\n"
	if got := run(newUsageCmd(), "codex"); got != wantEmpty {
		t.Fatalf("usage codex (no rows) output changed:\n%q\nwant\n%q", got, wantEmpty)
	}
}

// splitRows returns the header fields and the fields of the row for provider
// account from a tabwriter table.
func splitRows(t *testing.T, out, provider, account string) (header, row []string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	header = strings.Fields(lines[0])
	for _, l := range lines[1:] {
		if f := strings.Fields(l); len(f) > 1 && f[0] == provider && f[1] == account {
			return header, f
		}
	}
	t.Fatalf("no row for %s %s in %q", provider, account, out)
	return nil, nil
}

// TestListKeepsUnresolvableRowUnlessColumnsNeedDir: with HOME unset a "~/" dir
// cannot be resolved. The plain list keeps the row, as before; a column that
// needs the resolved dir (--status, which the status alias also uses) drops it.
func TestListKeepsUnresolvableRowUnlessColumnsNeedDir(t *testing.T) {
	path := withTempConfig(t)
	seedConfig(t, path, "~/aiacc-109-unresolvable", 0)
	t.Setenv("HOME", "")

	if plain := runList(t); !strings.Contains(plain, "aiacc-109-unresolvable") {
		t.Fatalf("plain list dropped the unresolvable row: %q", plain)
	}
	if withStatus := runList(t, "--status"); strings.Contains(withStatus, "work") {
		t.Fatalf("list --status kept a row it cannot resolve: %q", withStatus)
	}
}
