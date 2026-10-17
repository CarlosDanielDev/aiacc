package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/shell"
)

const foreignBody = "#!/bin/sh\n# my own script, tracked in dotfiles\nexec mine \"$@\"\n"

// foreignSetup is a hermetic install with one account "work" and a file at
// binDir/work that aiacc did not write. It returns the config path and that file.
func foreignSetup(t *testing.T) (cfgPath, foreign string) {
	t.Helper()
	home := hermeticHome(t)
	cfgPath = filepath.Join(home, "aiacc", "config.toml")
	if err := saveAccount(cfgPath, "claude", "work", filepath.Join(home, ".claude-work"), 0, "", "", ""); err != nil {
		t.Fatal(err)
	}
	foreign = filepath.Join(home, ".local", "bin", "work")
	if err := os.WriteFile(foreign, []byte(foreignBody), 0o755); err != nil {
		t.Fatal(err)
	}
	return cfgPath, foreign
}

func assertForeignIntact(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("foreign file is gone: %v", err)
	}
	if string(got) != foreignBody {
		t.Fatalf("foreign file was modified:\n%s", got)
	}
}

func TestSetupLeavesForeignLauncherAlone(t *testing.T) {
	cfgPath, foreign := foreignSetup(t)
	res, err := doSetup(cfgPath, false)
	if err != nil {
		t.Fatal(err)
	}
	assertForeignIntact(t, foreign)
	if !slices.Contains(res.Skipped, foreign) {
		t.Fatalf("setup did not report %s as skipped: %v", foreign, res.Skipped)
	}
	if slices.Contains(res.Names, "work") {
		t.Fatalf("skipped account still listed as installed: %v", res.Names)
	}
}

func TestSetupReportNamesForeignLauncher(t *testing.T) {
	cfgPath, foreign := foreignSetup(t)
	res, err := doSetup(cfgPath, false)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writeSetupReport(&out, res)
	text := out.String()
	if !strings.Contains(text, tildeize(foreign)) {
		t.Fatalf("warning does not name %s:\n%s", tildeize(foreign), text)
	}
	if !strings.Contains(text, "aiacc setup --replace") {
		t.Fatalf("warning does not say how to resolve it:\n%s", text)
	}
}

func TestSetupReplaceBacksUpForeignLauncher(t *testing.T) {
	cfgPath, foreign := foreignSetup(t)
	res, err := doSetup(cfgPath, true)
	if err != nil {
		t.Fatal(err)
	}
	bak := foreign + ".aiacc-bak"
	got, err := os.ReadFile(bak)
	if err != nil || string(got) != foreignBody {
		t.Fatalf("original not backed up to %s: %v\n%s", bak, err, got)
	}
	if !slices.Contains(res.Backups, bak) {
		t.Fatalf("setup did not report backup %s: %v", bak, res.Backups)
	}
	launcher, err := os.ReadFile(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(launcher), shell.LauncherMark) {
		t.Fatalf("takeover did not write an aiacc launcher:\n%s", launcher)
	}
}

func TestSyncLauncherRefusesForeignFile(t *testing.T) {
	cfgPath, foreign := foreignSetup(t)
	err := syncLauncher(cfgPath, "claude", "work")
	if err == nil || !strings.Contains(err.Error(), tildeize(foreign)) || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("want an error naming %s and the --replace fix, got %v", tildeize(foreign), err)
	}
	assertForeignIntact(t, foreign)
}

func TestRemoveLauncherKeepsForeignFile(t *testing.T) {
	cfgPath, foreign := foreignSetup(t)
	removeLauncher(cfgPath, "work")
	assertForeignIntact(t, foreign)
}

// Our own launchers stay refreshable: the guard must not turn a second setup
// into a skip.
func TestSetupRewritesOwnLauncher(t *testing.T) {
	home := hermeticHome(t)
	cfgPath := filepath.Join(home, "aiacc", "config.toml")
	if err := saveAccount(cfgPath, "claude", "work", filepath.Join(home, ".claude-work"), 0, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := doSetup(cfgPath, false); err != nil {
		t.Fatal(err)
	}
	res, err := doSetup(cfgPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 0 || !slices.Contains(res.Names, "work") {
		t.Fatalf("own launcher not refreshed: names=%v skipped=%v", res.Names, res.Skipped)
	}
}
