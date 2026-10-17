package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
)

// installedElsewhere runs setup while only installDir is on PATH, then puts
// laterDir first on PATH, as a user does after adding ~/.local/bin to their
// shell. The launcher stays where setup put it. Hermetic: HOME, PATH and the
// config all live in a temp dir.
func installedElsewhere(t *testing.T) (cfgPath, installDir, laterDir string) {
	t.Helper()
	home := t.TempDir()
	installDir = filepath.Join(home, "bin")
	laterDir = filepath.Join(home, ".local", "bin")
	for _, d := range []string{installDir, laterDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", installDir)
	cfgPath = filepath.Join(home, "aiacc", "config.toml")
	if err := saveAccount(cfgPath, "claude", "work", filepath.Join(home, ".claude-work"), 0, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := doSetup(cfgPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "work")); err != nil {
		t.Fatalf("setup did not write the launcher into %s: %v", installDir, err)
	}
	t.Setenv("PATH", laterDir+string(os.PathListSeparator)+installDir)
	return cfgPath, installDir, laterDir
}

func TestRemoveDeletesLauncherFromSetupDir(t *testing.T) {
	cfgPath, installDir, _ := installedElsewhere(t)
	if err := removeAccount(cfgPath, "claude", "work"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "work")); !os.IsNotExist(err) {
		t.Fatalf("launcher left in %s after remove (stat err=%v)", installDir, err)
	}
}

func TestSyncWritesLauncherToSetupDir(t *testing.T) {
	cfgPath, installDir, laterDir := installedElsewhere(t)
	if err := saveAccount(cfgPath, "claude", "play", filepath.Join(t.TempDir(), "play"), 0, "", "", ""); err != nil {
		t.Fatal(err)
	}
	syncLauncher(cfgPath, "claude", "play")
	if _, err := os.Stat(filepath.Join(installDir, "play")); err != nil {
		t.Fatalf("sync did not write the launcher into setup dir %s: %v", installDir, err)
	}
	if _, err := os.Stat(filepath.Join(laterDir, "play")); !os.IsNotExist(err) {
		t.Fatalf("sync wrote the launcher into %s instead (stat err=%v)", laterDir, err)
	}
}

func TestDoctorFindsOrphanInSetupDir(t *testing.T) {
	cfgPath, installDir, _ := installedElsewhere(t)
	// The account leaves the config without aiacc removing its launcher.
	if err := config.Update(cfgPath, func(c *config.Config) error {
		delete(c.Providers["claude"].Accounts, "work")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range checkLaunchers(c) {
		if f.sev == sevWarn && f.text == "1 launcher(s) for accounts aiacc no longer has" {
			return
		}
	}
	t.Fatalf("doctor did not report the orphan launcher left in %s", installDir)
}
