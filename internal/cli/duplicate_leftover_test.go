package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
)

// A name that already carries the provider prefix is not prefixed twice:
// `aiacc duplicate claude work claude-glm2` defaults to ~/.claude-glm2.
func TestDuplicateDefaultDirKeepsPrefixOnce(t *testing.T) {
	_, cfgPath, _ := dupFixture(t)
	if _, err := runDuplicate(t, "claude", "work", "claude-glm2"); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers["claude"].Accounts["claude-glm2"].Dir; got != "~/.claude-glm2" {
		t.Fatalf("dir = %q, want ~/.claude-glm2", got)
	}
}

// The line wizard defaults through the same rule, and its prompt names the
// real default instead of a ~/.claude-<name> that doubles the prefix.
func TestAddWizardDefaultDirKeepsPrefixOnce(t *testing.T) {
	home := hermeticHome(t)
	path := filepath.Join(home, "config.toml")
	var out bytes.Buffer
	if err := runAddWizard(bytes.NewBufferString("claude-glm2\n\n\n\n\n"), &out, path, addPrefill{}); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	prompt, _, _ := strings.Cut(out.String(), "Provider [")
	if !strings.Contains(prompt, "~/.claude-glm2") || strings.Contains(out.String(), "claude-claude") {
		t.Fatalf("dir prompt does not name ~/.claude-glm2:\n%s", out.String())
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.Providers["claude"].Accounts["claude-glm2"].Dir, filepath.Join(home, ".claude-glm2"); got != want {
		t.Fatalf("dir = %q, want %q", got, want)
	}
}

// seedLeftoverLinks leaves dir the way an add followed by `aiacc remove` does:
// holding only the shared-asset links linkProfile makes, no account on it.
func seedLeftoverLinks(t *testing.T, dir string) {
	t.Helper()
	linkProfile("claude", dir)
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) == 0 {
		t.Fatalf("leftover dir not seeded: %d entries, %v", len(ents), err)
	}
}

// A dir holding only aiacc's own shared links is taken over: the duplicate is
// registered there, the settings copy lands beside the links, and every link
// is left as it was.
func TestDuplicateAcceptsLeftoverLinks(t *testing.T) {
	home, cfgPath, _ := dupFixture(t)
	dir := filepath.Join(home, ".claude-glm2")
	seedLeftoverLinks(t, dir)

	if _, err := runDuplicate(t, "claude", "work", "claude-glm2"); err != nil {
		t.Fatalf("duplicate onto aiacc's own links: %v", err)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers["claude"].Accounts["claude-glm2"]; got.Dir != "~/.claude-glm2" || got.Settings != "~/.claude-glm2/glm.json" {
		t.Fatalf("claude-glm2 = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "glm.json")); err != nil {
		t.Fatalf("settings not copied: %v", err)
	}
	target, err := os.Readlink(filepath.Join(dir, "skills"))
	if err != nil || target != filepath.Join(home, ".claude", "skills") {
		t.Fatalf("skills link = %q, %v; want it untouched", target, err)
	}
}

// Anything aiacc would not have put there still refuses the dir, with nothing
// written anywhere: a real file, a real dir, a shared name linked elsewhere.
func TestDuplicateRefusesLeftoverWithOwnEntries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra func(t *testing.T, dir string)
	}{
		{"real file", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"real dir", func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, "projects"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"link elsewhere", func(t *testing.T, dir string) {
			link := filepath.Join(dir, "skills")
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), link); err != nil {
				t.Fatal(err)
			}
		}},
		{"unshared name linked into base", func(t *testing.T, dir string) {
			base := filepath.Join(filepath.Dir(dir), ".claude")
			if err := os.Symlink(filepath.Join(base, "skills"), filepath.Join(dir, "extra")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, cfgPath, _ := dupFixture(t)
			dir := filepath.Join(home, ".claude-glm2")
			seedLeftoverLinks(t, dir)
			tc.extra(t, dir)
			cfgBefore, _ := os.ReadFile(cfgPath)
			diskBefore := contentTreeOf(t, home)

			_, err := runDuplicate(t, "claude", "work", "claude-glm2")
			if err == nil || !strings.Contains(err.Error(), "~/.claude-glm2 is not empty") || !strings.Contains(err.Error(), "--dir") {
				t.Fatalf("err = %v, want the not-empty refusal naming --dir", err)
			}
			if cfgAfter, _ := os.ReadFile(cfgPath); !bytes.Equal(cfgBefore, cfgAfter) {
				t.Fatalf("config changed by a refusal")
			}
			if diskAfter := contentTreeOf(t, home); strings.Join(diskBefore, "\n") != strings.Join(diskAfter, "\n") {
				t.Fatalf("a refusal changed the disk")
			}
		})
	}
}

// The settings copy never writes through a leftover link: a source settings
// file named like a shared entry (settings.json) refuses the dir before any
// write, and the shared base file keeps its bytes.
func TestDuplicateLeftoverNeverOverwritesSettings(t *testing.T) {
	home, cfgPath, srcDir := dupFixture(t)
	if err := os.WriteFile(filepath.Join(srcDir, "settings.json"), []byte("{\"src\": true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	acct := c.Providers["claude"].Accounts["work"]
	acct.Settings = "~/.claude-work/settings.json"
	c.Providers["claude"].Accounts["work"] = acct
	if err := config.Save(cfgPath, c); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude-glm2")
	seedLeftoverLinks(t, dir)
	cfgBefore, _ := os.ReadFile(cfgPath)
	diskBefore := contentTreeOf(t, home)

	_, err = runDuplicate(t, "claude", "work", "claude-glm2")
	if err == nil || !strings.Contains(err.Error(), "settings.json") || !strings.Contains(err.Error(), "--dir") {
		t.Fatalf("err = %v, want a refusal naming settings.json and --dir", err)
	}
	if cfgAfter, _ := os.ReadFile(cfgPath); !bytes.Equal(cfgBefore, cfgAfter) {
		t.Fatalf("config changed by a refusal")
	}
	if diskAfter := contentTreeOf(t, home); strings.Join(diskBefore, "\n") != strings.Join(diskAfter, "\n") {
		t.Fatalf("a refusal changed the disk")
	}
}
