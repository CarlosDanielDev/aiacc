package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
)

func TestAddWizardRegistersAccount(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir()) // hermetic: don't read real ~/.claude
	path := filepath.Join(t.TempDir(), "config.toml")
	customDir := t.TempDir()
	in := bytes.NewBufferString("demo\n" + customDir + "\n")
	var out bytes.Buffer
	if err := runAddWizard(in, &out, path); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Providers["claude"].Accounts["demo"].Dir != customDir {
		t.Fatalf("account not registered: %+v", c.Providers["claude"])
	}
	if !strings.Contains(out.String(), "Registered 'demo'") {
		t.Fatalf("missing confirmation: %q", out.String())
	}
}

func TestAddWizardEmptyNameErrors(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := runAddWizard(bytes.NewBufferString("\n"), &bytes.Buffer{}, path); err == nil {
		t.Fatal("want error for empty name")
	}
}

// TestAddWizardDefaultsToClaude: an empty provider answer means claude — the
// historical behavior stays the default.
func TestAddWizardDefaultsToClaude(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	dir := t.TempDir()
	in := bytes.NewBufferString("demo\n" + dir + "\n\n\n")
	if err := runAddWizard(in, &bytes.Buffer{}, path, addPrefill{}); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Providers["claude"].Accounts["demo"].Dir != dir {
		t.Fatalf("claude account not registered: %+v", c.Providers)
	}
}

// TestAddWizardRegistersOtherProvider: the wizard asks which provider the
// account belongs to; codex has no settings flag, so it gets no settings
// question (three answers total).
func TestAddWizardRegistersOtherProvider(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	dir := t.TempDir()
	in := bytes.NewBufferString("work\n" + dir + "\ncodex\n")
	if err := runAddWizard(in, &bytes.Buffer{}, path, addPrefill{}); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Providers["codex"].Accounts["work"].Dir != dir {
		t.Fatalf("codex account not registered: %+v", c.Providers)
	}
}

// TestAddWizardUnknownProviderRefused: a typo must not silently register a
// junk provider (the bug class this wizard is being fixed for) — the valid
// names are listed instead.
func TestAddWizardUnknownProviderRefused(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	err := runAddWizard(bytes.NewBufferString("x\nfoo\nclade\n"), &bytes.Buffer{}, path, addPrefill{})
	if err == nil {
		t.Fatal("unknown provider was accepted")
	}
	for _, want := range []string{"clade", "claude", "codex", "aiacc add <provider> <account>", "--env", "--command"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not mention %q: %v", want, err)
		}
	}
}

// TestAddWizardSettingsKeyword: a piped keyword answer behaves exactly like
// the --settings flag — the skeleton is generated into the account dir and
// the generated path is what registers.
func TestAddWizardSettingsKeyword(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	dir := t.TempDir()
	in := bytes.NewBufferString("foo\n" + dir + "\nclaude\nglm\n")
	if err := runAddWizard(in, &bytes.Buffer{}, path, addPrefill{}); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	skeleton := filepath.Join(dir, "glm.json")
	if _, err := os.Stat(skeleton); err != nil {
		t.Fatalf("skeleton not written: %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers["claude"].Accounts["foo"].Settings; got != skeleton {
		t.Fatalf("settings = %q, want the generated path %q", got, skeleton)
	}
}

// TestAddWizardExpandsTildeDir: a literal ~ answer must not grow a "./~" tree
// in the working directory — the dir is created expanded while the account
// registers the as-typed form, matching both other add surfaces.
func TestAddWizardExpandsTildeDir(t *testing.T) {
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

	path := filepath.Join(tmpHome, "config.toml")
	in := bytes.NewBufferString("foo\n~/.claude-foo\nclaude\nglm\n")
	if err := runAddWizard(in, &bytes.Buffer{}, path, addPrefill{}); err != nil {
		t.Fatalf("wizard: %v", err)
	}

	if _, err := os.Stat(filepath.Join(work, "~")); err == nil {
		t.Fatalf("a literal ~ directory grew in the working directory")
	}
	expanded := filepath.Join(tmpHome, ".claude-foo", "glm.json")
	if _, err := os.Stat(expanded); err != nil {
		t.Fatalf("skeleton not at the expanded location %s: %v", expanded, err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	acct := c.Providers["claude"].Accounts["foo"]
	if acct.Dir != "~/.claude-foo" {
		t.Fatalf("dir = %q, want the as-typed form", acct.Dir)
	}
	if acct.Settings != "~/.claude-foo/glm.json" {
		t.Fatalf("settings = %q, want the as-typed form", acct.Settings)
	}
}

// TestAddWizardPrefillsFromFlags: a flag the user already gave preempts its
// question instead of being silently dropped — the answers are not consumed
// from stdin, so a piped script needs only the questions that remain.
func TestAddWizardPrefillsFromFlags(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	dir := t.TempDir()
	in := bytes.NewBufferString("work\n") // dir and settings come from the flags
	if err := runAddWizard(in, &bytes.Buffer{}, path, addPrefill{Dir: dir, Settings: "glm", Quota: 7}); err != nil {
		t.Fatalf("wizard: %v", err)
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
	if _, err := os.Stat(filepath.Join(dir, "glm.json")); err != nil {
		t.Fatalf("skeleton not written: %v", err)
	}
}

// assertLegacyAccount checks the outcome of the wizard's original two-line
// input: a claude account at dir, its launcher installed, and every shared
// asset linked into the new profile.
func assertLegacyAccount(t *testing.T, home, path, name, dir string) {
	t.Helper()
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers["claude"].Accounts[name].Dir; got != dir {
		t.Fatalf("claude account %q dir = %q, want %q (providers: %+v)", name, got, dir, c.Providers)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", name)); err != nil {
		t.Fatalf("launcher not synced: %v", err)
	}
	links := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			links++
		}
	}
	if want := len(provider.Presets["claude"].Shared); links != want {
		t.Fatalf("linked %d shared assets, want %d", links, want)
	}
}

// TestAddWizardLegacyTwoLinesOutcome: the wizard's original input — a name and a
// config dir, and nothing else — still registers a claude account at that dir,
// syncs its launcher and links the shared assets. A script that pipes those
// two lines must not break when the provider question was added after them.
func TestAddWizardLegacyTwoLinesOutcome(t *testing.T) {
	home := hermeticHome(t)
	path := filepath.Join(home, "config.toml")
	dir := filepath.Join(home, "legacy")
	if err := runAddWizard(bytes.NewBufferString("demo\n"+dir+"\n"), &bytes.Buffer{}, path, addPrefill{}); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	assertLegacyAccount(t, home, path, "demo", dir)
}

// TestAddWizardLegacyEmptyDirDefaults: the old "name then empty dir" input takes
// the claude default ~/.claude-<name> with the same outcome.
func TestAddWizardLegacyEmptyDirDefaults(t *testing.T) {
	home := hermeticHome(t)
	path := filepath.Join(home, "config.toml")
	if err := runAddWizard(bytes.NewBufferString("demo\n\n"), &bytes.Buffer{}, path, addPrefill{}); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	assertLegacyAccount(t, home, path, "demo", filepath.Join(home, ".claude-demo"))
}

// TestAddWizardSettingsNoneMeansNone: "none" is the settings prompt's default,
// so in any case it registers without settings instead of being refused as an
// unknown endpoint keyword.
func TestAddWizardSettingsNoneMeansNone(t *testing.T) {
	home := hermeticHome(t)
	path := filepath.Join(home, "config.toml")
	dir := filepath.Join(home, "none-acct")
	if err := runAddWizard(bytes.NewBufferString("foo\n"+dir+"\nclaude\nNone\n"), &bytes.Buffer{}, path, addPrefill{}); err != nil {
		t.Fatalf("wizard refused settings \"None\": %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers["claude"].Accounts["foo"].Settings; got != "" {
		t.Fatalf("settings = %q, want none", got)
	}
}

// TestAddWizardCustomProviderLaunchLine: a configured custom provider that has
// no launch command yet takes --command from the prefill when the account is
// saved, so the closing line must name that CLI, read after the save, rather
// than the provider's name.
func TestAddWizardCustomProviderLaunchLine(t *testing.T) {
	home := hermeticHome(t)
	path := filepath.Join(home, "config.toml")
	seeded := &config.Config{Providers: map[string]config.Provider{
		"myprov": {EnvVar: "MYCLI_HOME", Accounts: map[string]config.Account{}},
	}}
	if err := config.Save(path, seeded); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "mycli-acct")
	var out bytes.Buffer
	if err := runAddWizard(bytes.NewBufferString("work\nmyprov\n"), &out, path, addPrefill{Dir: dir, Command: "mycli"}); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	if !strings.Contains(out.String(), "Launch mycli as this profile:") {
		t.Fatalf("closing line names the wrong CLI:\n%s", out.String())
	}
}

// TestAddWizardUnknownProviderRegistersNothing: an unknown provider is refused
// with the actionable message, before anything is written — no account, no
// config dir, no launcher — whether or not --env and --command were given,
// since the wizard never creates a provider.
func TestAddWizardUnknownProviderRegistersNothing(t *testing.T) {
	cases := []struct {
		name string
		pre  addPrefill
	}{
		{"plain", addPrefill{}},
		{"with env and command", addPrefill{Env: "MYCLI_HOME", Command: "mycli"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := hermeticHome(t)
			path := filepath.Join(home, "config.toml")
			dir := filepath.Join(home, "newprov-acct")
			pre := tc.pre
			pre.Dir = dir
			err := runAddWizard(bytes.NewBufferString("work\nnewprov\n"), &bytes.Buffer{}, path, pre)
			if err == nil {
				t.Fatal("unknown provider was accepted")
			}
			if !strings.Contains(err.Error(), "aiacc add <provider> <account> --dir <dir> --env <ENV_VAR> --command <cli>") {
				t.Fatalf("refusal does not name the flag form that creates a provider: %v", err)
			}
			if _, statErr := os.Stat(dir); statErr == nil {
				t.Fatalf("config dir was created before the refusal: %s", dir)
			}
			if _, statErr := os.Stat(filepath.Join(home, ".local", "bin", "work")); statErr == nil {
				t.Fatal("a launcher was written before the refusal")
			}
			c, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(c.Providers) != 0 {
				t.Fatalf("something was registered before the refusal: %+v", c.Providers)
			}
		})
	}
}

// TestAddWizardShowsResolvedDefaultDir: a provider other than claude defaults the
// config dir to ~/.<provider>-<name>, which the dir question cannot know yet. The
// resolved dir must be shown before the account is registered under it.
func TestAddWizardShowsResolvedDefaultDir(t *testing.T) {
	home := hermeticHome(t)
	path := filepath.Join(home, "config.toml")
	var out bytes.Buffer
	if err := runAddWizard(bytes.NewBufferString("work\n\ncodex\n"), &out, path, addPrefill{}); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	want := filepath.Join(home, ".codex-work")
	if !strings.Contains(out.String(), "Config dir: "+want+" (default for codex)") {
		t.Fatalf("resolved default dir was not shown before registering:\n%s", out.String())
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers["codex"].Accounts["work"].Dir; got != want {
		t.Fatalf("codex account dir = %q, want %q", got, want)
	}
}

// TestAddWizardNotesIgnoredFlags: --env and --command only fill a provider that
// has neither yet. When the provider already has them, the wizard says which
// flag was ignored and what the provider already uses, instead of dropping it.
func TestAddWizardNotesIgnoredFlags(t *testing.T) {
	home := hermeticHome(t)
	path := filepath.Join(home, "config.toml")
	seeded := &config.Config{Providers: map[string]config.Provider{
		"myprov": {EnvVar: "MYCLI_HOME", Command: "mycli", Accounts: map[string]config.Account{}},
	}}
	if err := config.Save(path, seeded); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	pre := addPrefill{Dir: filepath.Join(home, "acct"), Env: "OTHER_HOME", Command: "other"}
	if err := runAddWizard(bytes.NewBufferString("work\nmyprov\n"), &out, path, pre); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	for _, want := range []string{
		`note: --env ignored — provider myprov already uses "MYCLI_HOME"`,
		`note: --command ignored — provider myprov already uses "mycli"`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in output:\n%s", want, out.String())
		}
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p := c.Providers["myprov"]; p.EnvVar != "MYCLI_HOME" || p.Command != "mycli" {
		t.Fatalf("existing provider changed: %+v", p)
	}
}

// TestAddWizardRefusesBadNameBeforeAnyWrite: a name the rules reject (here a
// path separator) is refused right after it is read, so no config dir, parent
// directory or launcher is left behind — even when the dir was left to default.
func TestAddWizardRefusesBadNameBeforeAnyWrite(t *testing.T) {
	home := hermeticHome(t)
	path := filepath.Join(home, "config.toml")
	err := runAddWizard(bytes.NewBufferString("a/b\n\n\n"), &bytes.Buffer{}, path, addPrefill{})
	if err == nil || !strings.Contains(err.Error(), "invalid account name") {
		t.Fatalf("bad name was not refused with the rule: %v", err)
	}
	for _, p := range []string{filepath.Join(home, ".claude-a"), filepath.Join(home, ".local", "bin", "a")} {
		if _, statErr := os.Stat(p); statErr == nil {
			t.Fatalf("residue left by a refused name: %s", p)
		}
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Providers) != 0 {
		t.Fatalf("something was registered: %+v", c.Providers)
	}
}

// TestAddWizardRefusedSettingsWritesNothing: a settings answer the rules refuse
// (a typo keyword, the custom keyword, or any settings value for a provider with
// no settings flag) is refused before the account dir is created, so nothing is
// left on disk or in the config.
func TestAddWizardRefusedSettingsWritesNothing(t *testing.T) {
	cases := []struct {
		name    string
		input   func(dir string) string
		pre     func(dir string) addPrefill
		wantErr string
	}{
		{
			name:    "typo keyword at the settings prompt",
			input:   func(d string) string { return "foo\n" + d + "\nclaude\nkimi\n" },
			wantErr: `"kimi"`,
		},
		{
			name:    "custom keyword at the settings prompt",
			input:   func(d string) string { return "foo\n" + d + "\nclaude\ncustom\n" },
			wantErr: "needs a base URL",
		},
		{
			name:    "prefilled --settings for a provider with no settings flag",
			input:   func(string) string { return "work\ncodex\n" },
			pre:     func(d string) addPrefill { return addPrefill{Dir: d, Settings: "glm"} },
			wantErr: "has no settings flag",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := hermeticHome(t)
			path := filepath.Join(home, "config.toml")
			dir := filepath.Join(home, "acct")
			var pre addPrefill
			if tc.pre != nil {
				pre = tc.pre(dir)
			}
			err := runAddWizard(bytes.NewBufferString(tc.input(dir)), &bytes.Buffer{}, path, pre)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want refusal mentioning %q, got %v", tc.wantErr, err)
			}
			if _, statErr := os.Stat(dir); statErr == nil {
				t.Fatalf("refused settings left the account dir behind: %s", dir)
			}
			c, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(c.Providers) != 0 {
				t.Fatalf("something was registered before the refusal: %+v", c.Providers)
			}
		})
	}
}

// TestMain runs the whole package under a temp HOME and a temp bin dir first on
// PATH (system dirs kept for any tool a test execs). Add, rename and remove
// write and delete launchers in the first writable PATH dir, so without this
// they reached the real ~/.local/bin of the machine running the suite.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "aiacc-cli-test")
	if err != nil {
		panic(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		panic(err)
	}
	os.Setenv("HOME", filepath.Join(root, "home"))
	os.Setenv("PATH", strings.Join([]string{bin, "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	// The developer's own Claude login and aiacc config must never be read.
	os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude-config"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	code := m.Run()
	os.RemoveAll(root)
	os.Exit(code)
}

// hermeticHome isolates the launcher install dir and the shared-asset base in
// a temp HOME with that bin dir as the only PATH entry, so syncLauncher and
// linkProfile land somewhere the test can inspect. The base ~/.claude is seeded
// with one authored asset per shared entry, so linkProfile has real links to make.
func hermeticHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	seedSharedBase(t, filepath.Join(home, ".claude"))
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	return home
}

// seedSharedBase writes one authored file or directory per claude shared entry
// (a name with an extension is a file, anything else a directory).
func seedSharedBase(t *testing.T, base string) {
	t.Helper()
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range provider.Presets["claude"].Shared {
		p := filepath.Join(base, name)
		if filepath.Ext(name) != "" {
			if err := os.WriteFile(p, []byte("authored"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "item"), []byte("authored"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// addSnapshot is everything a piped add is supposed to produce: the registered
// config, the installed launcher script, and the shared-asset symlinks — with
// the temp HOME normalized out so two runs are comparable.
type addSnapshot struct {
	cfg      config.Config
	launcher string
	links    map[string]string
}

func snapshotAdd(t *testing.T, home, cfgPath string) addSnapshot {
	t.Helper()
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".local", "bin", "work"))
	if err != nil {
		t.Fatalf("launcher: %v", err)
	}
	dir := filepath.Join(home, ".claude-work")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("account dir: %v", err)
	}
	links := map[string]string{}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name())); err != nil {
			t.Fatalf("link %s does not resolve: %v", e.Name(), err)
		}
		links[e.Name()] = strings.ReplaceAll(target, home, "$HOME")
	}
	// Every claude shared entry must be linked: an empty map would make the
	// parity comparison vacuous.
	if want := len(provider.Presets["claude"].Shared); len(links) != want {
		t.Fatalf("linked %d shared assets, want %d: %v", len(links), want, links)
	}
	return addSnapshot{*c, strings.ReplaceAll(string(raw), home, "$HOME"), links}
}

// TestAddWizardMatchesFlagPath: the wizard is not a second-class add — with
// the same typed answers it must produce the same config registration, the
// same launcher script, and the same shared-asset links as the flag path.
func TestAddWizardMatchesFlagPath(t *testing.T) {
	flagHome := hermeticHome(t)
	path := withTempConfig(t) // the flag run must not touch the real config
	add := newAddCmd()
	add.SetArgs([]string{"claude", "work", "--dir", "~/.claude-work", "--settings", "glm"})
	if err := add.Execute(); err != nil {
		t.Fatalf("flag add: %v", err)
	}
	flagSnap := snapshotAdd(t, flagHome, path)

	wizHome := hermeticHome(t)
	in := bytes.NewBufferString("work\n~/.claude-work\nclaude\nglm\n")
	if err := runAddWizard(in, &bytes.Buffer{}, filepath.Join(wizHome, "config.toml"), addPrefill{}); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	wizSnap := snapshotAdd(t, wizHome, filepath.Join(wizHome, "config.toml"))

	if !reflect.DeepEqual(flagSnap, wizSnap) {
		t.Fatalf("wizard output differs from the flag path:\nflag: %+v\nwizard: %+v", flagSnap, wizSnap)
	}
}

// The wizard refuses a name another provider owns before it asks the rest or
// creates the dir: a refused answer must leave no config dir behind.
func TestAddWizardRefusedNameCreatesNoDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	seedConfig(t, path, t.TempDir(), 0) // claude "work"
	dir := filepath.Join(t.TempDir(), "codex-work")

	err := runAddWizard(bytes.NewBufferString("work\n"+dir+"\ncodex\n"), &bytes.Buffer{}, path, addPrefill{})
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("want a refusal naming claude, got %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("refused wizard still created %s (stat err: %v)", dir, statErr)
	}
}
