package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
)

// The shell-rc edits are the only writes setup makes outside aiacc's own dirs.
// Each test points HOME at a temp dir, so RcPath resolves into it.

func TestAppendLineWritesOnceAndKeepsContent(t *testing.T) {
	const line = `export PATH='/x/bin':"$PATH"`
	cases := []struct {
		name     string
		existing string // "" means the file does not exist yet
		want     string
	}{
		{
			name: "creates the file and its parent dir",
			want: "\n# added by aiacc\n" + line + "\n",
		},
		{
			name:     "appends after existing content",
			existing: "alias ll='ls -l'\n",
			want:     "alias ll='ls -l'\n\n# added by aiacc\n" + line + "\n",
		},
		{
			name:     "leaves a file that already has the line untouched",
			existing: "# my own\n" + line + "\n",
			want:     "# my own\n" + line + "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := filepath.Join(t.TempDir(), "nested", "rc")
			if tc.existing != "" {
				if err := os.MkdirAll(filepath.Dir(rc), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(rc, []byte(tc.existing), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 { // the second call is the idempotency check
				if err := appendLine(rc, line); err != nil {
					t.Fatalf("appendLine: %v", err)
				}
			}
			got, err := os.ReadFile(rc)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("rc after two appendLine calls:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

func TestFileContains(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rc")
	if fileContains(p, "anything") {
		t.Fatal("a missing file contains nothing")
	}
	if err := os.WriteFile(p, []byte("export FOO=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileContains(p, "export FOO=1") {
		t.Error("present substring not found")
	}
	if fileContains(p, "export BAR") {
		t.Error("absent substring reported as present")
	}
}

// ensureOnPath is what setup runs when no writable dir is on PATH. It must put
// the PATH line into the rc of the shell the user actually runs, exactly once
// no matter how often setup runs.
func TestEnsureOnPathWritesOneLineToTheUsersShellRc(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish"} {
		t.Run(sh, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("SHELL", "/usr/local/bin/"+sh)
			binDir := filepath.Join(home, ".local", "bin")

			want, err := shell.PathLine(sh, binDir)
			if err != nil {
				t.Fatal(err)
			}
			rc, err := shell.RcPath(sh)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(rc, home) {
				t.Fatalf("rc %s is outside the temp HOME %s", rc, home)
			}

			ensureOnPath(binDir)
			ensureOnPath(binDir)

			got, err := os.ReadFile(rc)
			if err != nil {
				t.Fatalf("ensureOnPath wrote no rc: %v", err)
			}
			if n := strings.Count(string(got), want); n != 1 {
				t.Fatalf("PATH line appears %d times in %s, want 1:\n%s", n, rc, got)
			}
		})
	}
}

// launcherBinDir decides where every launcher lands. Order matters: a
// HOME-owned writable dir on PATH beats an unrelated one that happens to come
// first, because that is the dir a user would expect to find their commands in.
func TestLauncherBinDirPrefersHomeOwnedWritableDirOnPath(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, ".local", "bin")
	custom := filepath.Join(home, "tools")
	other := t.TempDir() // outside HOME, writable
	for _, d := range []string{local, custom} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	missing := filepath.Join(t.TempDir(), "nope")

	cases := []struct {
		name    string
		path    []string
		wantDir string
		wantOn  bool
	}{
		{"preferred HOME dir on PATH", []string{other, local}, local, true},
		{"HOME-owned dir beats an earlier outside dir", []string{other, custom}, custom, true},
		{"only an outside dir is writable", []string{other}, other, true},
		{"nothing writable on PATH falls back to ~/.local/bin", []string{missing}, local, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("PATH", strings.Join(tc.path, string(os.PathListSeparator)))
			dir, onPath := launcherBinDir()
			if dir != tc.wantDir || onPath != tc.wantOn {
				t.Fatalf("launcherBinDir() = (%s, %v), want (%s, %v)", dir, onPath, tc.wantDir, tc.wantOn)
			}
		})
	}
}

// One real doSetup into a temp HOME: the launcher is written, and the dir it
// went into is recorded in the config so later commands find it again.
func TestDoSetupWritesLaunchersAndRecordsBinDir(t *testing.T) {
	home := hermeticHome(t)
	cfgPath := filepath.Join(home, "aiacc", "config.toml")
	if err := saveAccount(cfgPath, "claude", "work", filepath.Join(home, ".claude-work"), 0, "", "", ""); err != nil {
		t.Fatal(err)
	}
	res, err := doSetup(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, ".local", "bin")
	if !res.WorksNow {
		t.Errorf("bin dir is on PATH, so the commands should work now")
	}
	if _, err := os.Stat(filepath.Join(bin, "work")); err != nil {
		t.Fatalf("launcher not written: %v", err)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if c.BinDir != bin {
		t.Fatalf("bin_dir recorded as %q, want %q", c.BinDir, bin)
	}
}
