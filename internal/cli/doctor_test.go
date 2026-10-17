package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
)

// bin creates a directory holding executable stubs named by cmds, and puts it
// first on PATH for the test.
func bin(t *testing.T, cmds ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, c := range cmds {
		script := "#!/bin/sh\necho '9.9.9 (" + c + " from " + filepath.Base(dir) + ")'\n"
		if err := os.WriteFile(filepath.Join(dir, c), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func setPath(t *testing.T, dirs ...string) {
	t.Helper()
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

func TestLookAllFindsEveryInstallInPathOrder(t *testing.T) {
	first, second := bin(t, "claude"), bin(t, "claude")
	setPath(t, first, second)

	got := lookAll("claude")
	if len(got) != 2 {
		t.Fatalf("want 2 matches, got %d: %v", len(got), got)
	}
	if got[0] != filepath.Join(first, "claude") {
		t.Errorf("first match must be the one PATH order picks: got %s", got[0])
	}
	if got[1] != filepath.Join(second, "claude") {
		t.Errorf("second match missing: got %v", got)
	}
}

// A second install is the silent hazard: both work, PATH order alone decides.
func TestCheckCommandsWarnsOnTwoInstalls(t *testing.T) {
	setPath(t, bin(t, "claude"), bin(t, "claude"))

	f := checkCommands(cfg(t, "claude", "work", t.TempDir()))
	if len(f) != 1 {
		t.Fatalf("want 1 finding, got %d", len(f))
	}
	if f[0].sev != sevWarn {
		t.Errorf("two installs must warn, got %v", f[0].sev)
	}
	if !strings.Contains(f[0].text, "2 installs") {
		t.Errorf("finding should name the count: %q", f[0].text)
	}
}

func TestCheckCommandsOKOnSingleInstall(t *testing.T) {
	setPath(t, bin(t, "claude"))

	f := checkCommands(cfg(t, "claude", "work", t.TempDir()))
	if len(f) != 1 || f[0].sev != sevOK {
		t.Fatalf("one install must be OK, got %+v", f)
	}
}

// The alias-only install: nothing on PATH, but the binary is right there. The
// launcher is a /bin/sh script and cannot see the shell alias, so doctor has to
// find the file and say where it is.
func TestUnresolvedCommandFindsMigrateInstallerLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setPath(t, t.TempDir()) // empty: nothing resolves

	local := filepath.Join(home, ".claude", "local")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	f := unresolvedCommand("claude", "claude")
	if f.sev != sevFail {
		t.Errorf("an unresolvable command is broken, want sevFail, got %v", f.sev)
	}
	joined := strings.Join(f.detail, "\n")
	if !strings.Contains(joined, filepath.Join(".claude", "local", "claude")) {
		t.Errorf("detail must name the install PATH cannot reach:\n%s", joined)
	}
	if !strings.Contains(joined, "alias") {
		t.Errorf("detail must explain why the launcher can't see it:\n%s", joined)
	}
}

func TestCheckLauncherStaleWhenDirChanged(t *testing.T) {
	binDir := t.TempDir()
	setPath(t, binDir)
	old, err := shell.LauncherScript("claude", "CLAUDE_CONFIG_DIR", "/old/dir")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "work"), []byte(old), 0o755); err != nil {
		t.Fatal(err)
	}

	f := checkLauncher("work", "claude", "CLAUDE_CONFIG_DIR", "/new/dir", nil)
	if f.sev != sevFail {
		t.Fatalf("a launcher pointing at the wrong dir is broken, got %v: %+v", f.sev, f)
	}
	if !strings.Contains(f.text, "stale") {
		t.Errorf("say it is stale: %q", f.text)
	}
}

func TestCheckLauncherOKWhenCurrent(t *testing.T) {
	binDir := t.TempDir()
	setPath(t, binDir)
	cur, err := shell.LauncherScript("claude", "CLAUDE_CONFIG_DIR", "/the/dir")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "work"), []byte(cur), 0o755); err != nil {
		t.Fatal(err)
	}

	if f := checkLauncher("work", "claude", "CLAUDE_CONFIG_DIR", "/the/dir", nil); f.sev != sevOK {
		t.Fatalf("a current launcher is fine, got %v: %+v", f.sev, f)
	}
}

func TestCheckLauncherMissing(t *testing.T) {
	setPath(t, t.TempDir())
	if f := checkLauncher("work", "claude", "CLAUDE_CONFIG_DIR", "/the/dir", nil); f.sev != sevFail {
		t.Fatalf("no launcher on PATH is broken, got %v", f.sev)
	}
}

// A command that shadows the launcher name is someone else's file. Report it,
// but never call it stale and never offer to rewrite it.
func TestCheckLauncherWarnsOnForeignCommand(t *testing.T) {
	binDir := bin(t, "work")
	setPath(t, binDir)

	f := checkLauncher("work", "claude", "CLAUDE_CONFIG_DIR", "/the/dir", nil)
	if f.sev != sevWarn {
		t.Fatalf("a foreign command shadowing the name is a warning, got %v", f.sev)
	}
	if !strings.Contains(f.text, "not an aiacc launcher") {
		t.Errorf("must not blame a file aiacc did not write: %q", f.text)
	}
}

func TestTallyAndSummary(t *testing.T) {
	secs := []section{{"a", []finding{{sev: sevOK}, {sev: sevWarn}}}}
	if fails, warns := tally(secs); fails != 0 || warns != 1 {
		t.Errorf("tally = %d, %d, want 0, 1", fails, warns)
	}
	if got := summary(secs); !strings.Contains(got, "nothing broken") {
		t.Errorf("summary = %q, want the nothing-broken wording", got)
	}

	secs[0].findings = append(secs[0].findings, finding{sev: sevFail})
	if fails, _ := tally(secs); fails != 1 {
		t.Errorf("tally fails = %d, want 1", fails)
	}
	if got := summary(secs); !strings.Contains(got, "1 broken") {
		t.Errorf("summary = %q, want the broken count", got)
	}
}

// cfg builds a one-account config for a provider.
func cfg(t *testing.T, providerName, account, dir string) *config.Config {
	t.Helper()
	return &config.Config{Providers: map[string]config.Provider{
		providerName: {
			EnvVar:   "CLAUDE_CONFIG_DIR",
			Command:  "claude",
			Accounts: map[string]config.Account{account: {Dir: dir}},
		},
	}}
}

// The migrate-installer location belongs to Claude's preset: it is probed for
// claude, and never invented for another provider.
func TestProbeInstallsUsesProviderLocations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setPath(t, t.TempDir())
	for _, c := range []string{"claude", "codex"} {
		dir := filepath.Join(home, "."+c, "local")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, c), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Homebrew prefixes are machine-wide, so assert on the HOME paths only.
	if got := probeInstalls("claude", "claude"); !slices.Contains(got, filepath.Join(home, ".claude", "local", "claude")) {
		t.Errorf("claude probe = %v, want the migrate-installer path", got)
	}
	if got := probeInstalls("codex", "codex"); slices.Contains(got, filepath.Join(home, ".codex", "local", "codex")) {
		t.Errorf("codex has no such install location, got %v", got)
	}
}

// Adding a settings file to an account makes its old launcher stale: until
// setup rewrites it, the profile still talks to the default endpoint.
func TestCheckLauncherStaleWithoutSettingsArgs(t *testing.T) {
	binDir := t.TempDir()
	setPath(t, binDir)
	old, err := shell.LauncherScript("claude", "CLAUDE_CONFIG_DIR", "/d")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "glm"), []byte(old), 0o755); err != nil {
		t.Fatal(err)
	}
	f := checkLauncher("glm", "claude", "CLAUDE_CONFIG_DIR", "/d", []string{"--settings", "/s.json"})
	if f.sev != sevFail || !strings.Contains(f.detail[0], "--settings /s.json") {
		t.Fatalf("want stale naming the settings flag, got %+v", f)
	}
}

func TestCheckSettingsFile(t *testing.T) {
	if _, ok := checkSettingsFile("w", ""); ok {
		t.Error("no settings file, nothing to report")
	}
	p := filepath.Join(t.TempDir(), "glm.json")
	if f, ok := checkSettingsFile("glm", p); !ok || f.sev != sevFail {
		t.Errorf("a missing settings file is broken, got %+v", f)
	}
	if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, ok := checkSettingsFile("glm", p); !ok || f.sev != sevWarn {
		t.Errorf("a group/world-readable settings file warns, got %+v", f)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := checkSettingsFile("glm", p); ok {
		t.Error("a private settings file is fine")
	}
}

// Names are global to the launcher bin, so two providers sharing one account
// name is a collision, not a stale launcher: doctor must say so, and must not
// send the user to `aiacc setup`, which would rewrite the same shared file.
func TestCheckLaunchersNamesCrossProviderCollision(t *testing.T) {
	binDir := t.TempDir()
	setPath(t, binDir)
	c := &config.Config{Providers: map[string]config.Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude", Accounts: map[string]config.Account{
			"work": {Dir: t.TempDir()},
		}},
		"codex": {EnvVar: "CODEX_HOME", Command: "codex", Accounts: map[string]config.Account{
			"work": {Dir: t.TempDir()},
		}},
	}}

	// One finding for the name, and it is the collision: no per-provider launcher
	// verdict (stale, missing, ...) may be issued for a name that has one file.
	var texts []string
	for _, f := range checkLaunchers(c) {
		if strings.HasPrefix(f.text, "work:") {
			texts = append(texts, f.text)
		}
	}
	if len(texts) != 1 || !strings.Contains(texts[0], "claude and codex") {
		t.Fatalf("want exactly one finding naming claude and codex for work, got %q", texts)
	}
}

// A settings path that names a directory is broken whatever its mode: the
// launcher hands the directory to the CLI as a settings file. A 0700 directory
// must not pass silently, and a 0755 one must fail rather than merely warn.
func TestCheckSettingsFileDirectory(t *testing.T) {
	for _, mode := range []os.FileMode{0o700, 0o755} {
		dir := filepath.Join(t.TempDir(), "claude-glm")
		if err := os.Mkdir(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, mode); err != nil { // Mkdir applies the umask
			t.Fatal(err)
		}
		f, ok := checkSettingsFile("glm", dir)
		if !ok || f.sev != sevFail || !strings.Contains(f.text, "settings path is a directory") {
			t.Errorf("mode %o: want a failing finding naming the directory, got %+v", mode, f)
		}
	}
}

// A provider with a command but no env var is unlaunchable forever, and setup
// skips it silently, so doctor is the one place that can say so. It must fail
// once per provider (not once per account) and name the command that fixes it.
func TestCheckLaunchersReportsProviderWithoutEnvOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setPath(t, t.TempDir())
	c := &config.Config{Providers: map[string]config.Provider{
		"custom": {Command: "mytool", Accounts: map[string]config.Account{
			"play":  {Dir: filepath.Join(home, "a")},
			"other": {Dir: filepath.Join(home, "b")},
		}},
	}}
	var hits []finding
	for _, f := range checkLaunchers(c) {
		if strings.HasPrefix(f.text, "custom:") {
			hits = append(hits, f)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("want exactly one finding for the no-env provider, got %d: %+v", len(hits), hits)
	}
	if hits[0].sev != sevFail || !strings.Contains(hits[0].text, "no env var configured") {
		t.Fatalf("want a failing 'no env var configured' finding, got %+v", hits[0])
	}
	if !strings.Contains(strings.Join(hits[0].detail, "\n"), "aiacc add custom <account>") {
		t.Fatalf("finding does not name the fix command: %v", hits[0].detail)
	}
}

// doctor is a CI gate: a broken config must surface as errUnhealthy (non-zero
// exit), and a clean one must not. Tally alone is not enough; the RunE wiring
// is what turns a finding into an exit code.
func TestDoctorExitCodeTracksBrokenFindings(t *testing.T) {
	path := withTempConfig(t)
	broken := &config.Config{Providers: map[string]config.Provider{"claude": {
		EnvVar:  "CLAUDE_CONFIG_DIR",
		Command: "sh",
		Accounts: map[string]config.Account{"work": {
			Dir:      t.TempDir(),
			Settings: filepath.Join(t.TempDir(), "missing.json"),
		}},
	}}}
	if err := config.Save(path, broken); err != nil {
		t.Fatal(err)
	}
	cmd := newDoctorCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); !errors.Is(err, errUnhealthy) {
		t.Fatalf("broken config: doctor returned %v, want errUnhealthy", err)
	}

	if err := config.Save(path, &config.Config{Providers: map[string]config.Provider{}}); err != nil {
		t.Fatal(err)
	}
	cmd = newDoctorCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("clean config: doctor returned %v, want nil", err)
	}
}

// A launcher-shaped file aiacc wrote for a removed account is an orphan. A
// user's own script in the same dir, a registered launcher, and an oversized
// file carrying the mark are not: doctor must never blame them.
func TestOrphanLaunchersFlagsOnlyAiaccLeftovers(t *testing.T) {
	binDir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("old", shell.LauncherMark+"\nexec claude\n")
	write("mine", "#!/bin/sh\necho my own script\n")
	write("kept", shell.LauncherMark+"\nexec claude\n")
	write("huge", shell.LauncherMark+strings.Repeat("x", maxLauncherSize))

	f := orphanLaunchers(binDir, map[string]bool{"kept": true})
	if len(f) != 1 {
		t.Fatalf("want one finding, got %+v", f)
	}
	if f[0].sev != sevWarn || f[0].text != "1 launcher(s) for accounts aiacc no longer has" {
		t.Fatalf("finding = %+v", f[0])
	}
	detail := strings.Join(f[0].detail, "\n")
	if !strings.Contains(detail, "  old") {
		t.Errorf("the aiacc leftover must be named: %q", detail)
	}
	for _, name := range []string{"mine", "kept", "huge"} {
		if strings.Contains(detail, name) {
			t.Errorf("doctor blamed %q: %q", name, detail)
		}
	}
}
