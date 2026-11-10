package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
)

// editFixtureProfile registers claude/glm at ~/.glm-dir with a settings
// skeleton (registered as typed, tilde kept) and installs the launcher into a
// bin dir on PATH. Everything lives in a temp HOME.
func editFixtureProfile(t *testing.T) (cfgPath, binDir, home string) {
	t.Helper()
	cfgPath, binDir, home = editFixtureAccount(t, "glm", true)
	return cfgPath, binDir, home
}

// editFixtureAccount registers claude/<name> at ~/.<name>-dir, with a settings
// skeleton when withSettings is set, and installs the launcher into a bin dir
// on PATH. Everything lives in a temp HOME.
func editFixtureAccount(t *testing.T, name string, withSettings bool) (cfgPath, binDir, home string) {
	t.Helper()
	home = t.TempDir()
	binDir = filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	cfgPath = filepath.Join(home, "aiacc", "config.toml")
	dir := "~/.claude-" + name
	var settings string
	if withSettings {
		var err error
		settings, err = resolveSettings("claude", dir, "glm")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := saveAccount(cfgPath, "claude", name, dir, 0, "", "", settings); err != nil {
		t.Fatal(err)
	}
	if _, err := doSetup(cfgPath, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(binDir, name)); err != nil {
		t.Fatalf("setup did not write the launcher: %v", err)
	}
	return cfgPath, binDir, home
}

// editRow is the picker row for provider/account, straight from the config.
func editRow(t *testing.T, cfgPath, providerName, account string) tui.Row {
	t.Helper()
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range collectRows(c, "") {
		if row.Provider == providerName && row.Account == account {
			return row
		}
	}
	t.Fatalf("%s/%s is not a registered profile", providerName, account)
	return tui.Row{}
}

// driveEditScreen replaces the edit screen with a fake that records the prefill
// and hands answer's dir and settings to the real submit, as the screen does on
// Enter.
func driveEditScreen(t *testing.T, answer func(f tui.EditForm) (string, string), seen *tui.EditForm) {
	t.Helper()
	old := editScreen
	editScreen = func(account, path string, f tui.EditForm, submit func(dir, settings string) error) (bool, error) {
		if seen != nil {
			*seen = f
		}
		dir, settings := answer(f)
		err := submit(dir, settings)
		return err == nil, err
	}
	t.Cleanup(func() { editScreen = old })
}

// TestEditPrefillsStoredValuesAsTyped: the form arrives with the stored dir and
// settings path exactly as the config holds them — tilde never expanded — and
// the env rows when the file is readable.
func TestEditPrefillsStoredValuesAsTyped(t *testing.T) {
	cfgPath, _, _ := editFixtureProfile(t)
	var seen tui.EditForm
	driveEditScreen(t, func(f tui.EditForm) (string, string) { return f.Dir, f.Settings }, &seen)
	if err := runEditTUI(cfgPath, editRow(t, cfgPath, "claude", "glm")); err != nil {
		t.Fatal(err)
	}
	if seen.Dir != "~/.claude-glm" {
		t.Fatalf("dir prefill = %q, want the stored as-typed value", seen.Dir)
	}
	wantSettings := filepath.Join("~/.claude-glm", provider.Presets["claude"].Endpoints["glm"].Filename)
	if seen.Settings != wantSettings {
		t.Fatalf("settings prefill = %q, want the stored as-typed value %q", seen.Settings, wantSettings)
	}
	if !seen.File {
		t.Fatal("readable settings file did not open the env editor")
	}
}

// TestEditDirUpdatesConfigAndLauncher: a dir edit stores the new dir as typed,
// creates it with the shared links, and re-syncs the launcher so its script
// carries the new dir — aiacc doctor stays green right after the edit.
func TestEditDirUpdatesConfigAndLauncher(t *testing.T) {
	cfgPath, binDir, home := editFixtureProfile(t)
	const newDir = "~/.claude-glm-moved"
	driveEditScreen(t, func(f tui.EditForm) (string, string) { return newDir, f.Settings }, nil)
	if err := runEditTUI(cfgPath, editRow(t, cfgPath, "claude", "glm")); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	acct := c.Providers["claude"].Accounts["glm"]
	if acct.Dir != newDir {
		t.Fatalf("stored dir = %q, want %q as typed", acct.Dir, newDir)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude-glm-moved")); err != nil {
		t.Fatalf("new dir not created: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(binDir, "glm"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), filepath.Join(home, ".claude-glm-moved")) {
		t.Fatalf("launcher still points at the old dir:\n%s", raw)
	}
	// the settings link survives the move: LaunchArgs still resolve
	if got := provider.LaunchArgs(c, "claude", "glm"); len(got) != 2 {
		t.Fatalf("LaunchArgs = %v, want the settings flag and file", got)
	}
}

// TestEditClearingSettingsDropsLaunchArg: an empty settings field clears the
// registration, and the launcher is re-synced without the --settings arg.
func TestEditClearingSettingsDropsLaunchArg(t *testing.T) {
	cfgPath, binDir, _ := editFixtureProfile(t)
	driveEditScreen(t, func(f tui.EditForm) (string, string) { return f.Dir, "" }, nil)
	if err := runEditTUI(cfgPath, editRow(t, cfgPath, "claude", "glm")); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if acct := c.Providers["claude"].Accounts["glm"]; acct.Settings != "" {
		t.Fatalf("stored settings = %q, want cleared", acct.Settings)
	}
	if got := provider.LaunchArgs(c, "claude", "glm"); got != nil {
		t.Fatalf("LaunchArgs = %v, want none after clearing", got)
	}
	raw, err := os.ReadFile(filepath.Join(binDir, "glm"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "--settings") {
		t.Fatalf("launcher still passes --settings:\n%s", raw)
	}
}

// TestEditRefusesSettingsDirKeepsRegistration: a settings value that names a
// directory is refused by the same rule the add path uses — the screen gets
// the refusal and the stored registration is untouched.
func TestEditRefusesSettingsDirKeepsRegistration(t *testing.T) {
	cfgPath, _, _ := editFixtureProfile(t)
	aDir := t.TempDir()
	var got error
	old := editScreen
	editScreen = func(account, path string, f tui.EditForm, submit func(dir, settings string) error) (bool, error) {
		got = submit(f.Dir, aDir)
		return false, nil // the screen stays open on a refusal
	}
	t.Cleanup(func() { editScreen = old })
	if err := runEditTUI(cfgPath, editRow(t, cfgPath, "claude", "glm")); err != nil {
		t.Fatal(err)
	}
	if got == nil || !strings.Contains(got.Error(), "is a directory") {
		t.Fatalf("submit error = %v, want the settings-dir refusal", got)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	acct := c.Providers["claude"].Accounts["glm"]
	if acct.Dir != "~/.claude-glm" || acct.Settings != filepath.Join("~/.claude-glm", provider.Presets["claude"].Endpoints["glm"].Filename) {
		t.Fatalf("registration changed by a refused save: %+v", acct)
	}
}

// TestEditWithoutSettingsGainsOne: a profile registered without a settings file
// opens the registration-only editor (no env rows), and a keyword answer gains
// one — the skeleton lands in the profile dir and LaunchArgs carry it.
func TestEditWithoutSettingsGainsOne(t *testing.T) {
	cfgPath, _, home := editFixtureAccount(t, "bare", false)
	var seen tui.EditForm
	driveEditScreen(t, func(f tui.EditForm) (string, string) { return f.Dir, "glm" }, &seen)
	if err := runEditTUI(cfgPath, editRow(t, cfgPath, "claude", "bare")); err != nil {
		t.Fatal(err)
	}
	if seen.File || seen.Settings != "" {
		t.Fatalf("prefill = %+v, want registration-only (no settings file)", seen)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	acct := c.Providers["claude"].Accounts["bare"]
	want := filepath.Join("~/.claude-bare", provider.Presets["claude"].Endpoints["glm"].Filename)
	if acct.Settings != want {
		t.Fatalf("stored settings = %q, want the skeleton path as typed (%q)", acct.Settings, want)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude-bare", provider.Presets["claude"].Endpoints["glm"].Filename)); err != nil {
		t.Fatalf("skeleton not created: %v", err)
	}
	if got := provider.LaunchArgs(c, "claude", "bare"); len(got) != 2 {
		t.Fatalf("LaunchArgs = %v, want the settings flag and file", got)
	}
}

// TestEditSecondKeywordEnterIsANoop: the screen keeps the typed keyword across
// a refused file save, so the second Enter submits it again. The registration
// must treat a keyword that already resolves to the stored skeleton as a no-op
// — regenerating would refuse on the existing file and strand the user.
func TestEditSecondKeywordEnterIsANoop(t *testing.T) {
	cfgPath, _, _ := editFixtureProfile(t)
	calls := 0
	var second error
	old := editScreen
	editScreen = func(account, path string, f tui.EditForm, submit func(dir, settings string) error) (bool, error) {
		calls++
		err := submit(f.Dir, "glm")
		if calls == 2 {
			second = err
		}
		return err == nil, err
	}
	t.Cleanup(func() { editScreen = old })
	if err := runEditTUI(cfgPath, editRow(t, cfgPath, "claude", "glm")); err != nil {
		t.Fatal(err)
	}
	if second != nil {
		t.Fatalf("second Enter refused: %v", second)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("~/.claude-glm", provider.Presets["claude"].Endpoints["glm"].Filename)
	if acct := c.Providers["claude"].Accounts["glm"]; acct.Settings != want {
		t.Fatalf("stored settings = %q, want the untouched skeleton path %q", acct.Settings, want)
	}
}

// TestEditEnvOnlySaveLeavesAMissingDirAlone: with the registration unchanged,
// an env-only save rewrites neither the config nor the disk layout — a missing
// dir is not silently recreated by a save the user meant for the file. No
// `aiacc setup` here: setup itself creates the account dir for its shared
// links, which would mask the save's behavior.
func TestEditEnvOnlySaveLeavesAMissingDirAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, "aiacc", "config.toml")
	// The settings file lives outside the dir, so the env editor opens while
	// the dir itself is gone.
	if err := os.WriteFile(filepath.Join(home, ".settings.json"), []byte(`{"env":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveAccount(cfgPath, "claude", "gone", "~/.claude-gone", 0, "", "", "~/.settings.json"); err != nil {
		t.Fatal(err)
	}
	driveEditScreen(t, func(f tui.EditForm) (string, string) { return f.Dir, f.Settings }, nil)
	if err := runEditTUI(cfgPath, editRow(t, cfgPath, "claude", "gone")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude-gone")); !os.IsNotExist(err) {
		t.Fatalf("env-only save recreated the missing dir (stat err=%v)", err)
	}
}

// TestEditUnreadableFileStillEditsRegistration: a registered settings file that
// cannot be read no longer dead-ends in a message screen — the registration
// rows stay editable so a broken link can be repaired.
func TestEditUnreadableFileStillEditsRegistration(t *testing.T) {
	cfgPath, _, home := editFixtureProfile(t)
	name := provider.Presets["claude"].Endpoints["glm"].Filename
	if err := os.WriteFile(filepath.Join(home, ".claude-glm", name), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	const newDir = "~/.claude-glm-moved"
	driveEditScreen(t, func(f tui.EditForm) (string, string) { return newDir, f.Settings }, nil)
	if err := runEditTUI(cfgPath, editRow(t, cfgPath, "claude", "glm")); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if acct := c.Providers["claude"].Accounts["glm"]; acct.Dir != newDir {
		t.Fatalf("stored dir = %q, want %q", acct.Dir, newDir)
	}
}
