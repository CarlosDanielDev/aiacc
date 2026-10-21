package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
)

// dupFixture registers claude/work at ~/.claude-work in a hermetic HOME, with a
// quota, a settings file, and the identity a duplicate must never carry: the
// login file, the transcripts and the prompt history.
func dupFixture(t *testing.T) (home, cfgPath, srcDir string) {
	t.Helper()
	home = hermeticHome(t)
	cfgPath = withTempConfig(t)
	srcDir = filepath.Join(home, ".claude-work")
	for _, p := range []string{
		filepath.Join(srcDir, ".credentials.json"),
		filepath.Join(srcDir, "history.jsonl"),
		filepath.Join(srcDir, "projects", "-repo", "s.jsonl"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("identity"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(srcDir, "glm.json"), []byte("{\"env\": {\"K\": \"v\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Providers: map[string]config.Provider{
		"claude": {Accounts: map[string]config.Account{
			"work":  {Dir: "~/.claude-work", Quota: 7, Settings: "~/.claude-work/glm.json"},
			"taken": {Dir: filepath.Join(home, ".claude-taken")},
		}},
	}}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	return home, cfgPath, srcDir
}

func runDuplicate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newDuplicateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// The clone carries src's quota and a byte copy of its settings file, private
// to the new dir, registered under the new dir.
func TestDuplicateCopiesQuotaAndSettings(t *testing.T) {
	home, cfgPath, srcDir := dupFixture(t)

	out, err := runDuplicate(t, "claude", "work", "work2")
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	got := c.Providers["claude"].Accounts["work2"]
	want := config.Account{Dir: "~/.claude-work2", Quota: 7, Settings: "~/.claude-work2/glm.json"}
	if got != want {
		t.Fatalf("work2 = %+v, want %+v", got, want)
	}
	srcBytes, _ := os.ReadFile(filepath.Join(srcDir, "glm.json"))
	dst := filepath.Join(home, ".claude-work2", "glm.json")
	dstBytes, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(srcBytes, dstBytes) {
		t.Fatalf("settings copy = %q (%v), want %q", dstBytes, err, srcBytes)
	}
	info, _ := os.Stat(dst)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings copy mode = %v, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "work2")); err != nil {
		t.Fatalf("launcher not synced: %v", err)
	}
	if fi, err := os.Lstat(filepath.Join(home, ".claude-work2", "skills")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("shared assets not linked: %v", err)
	}
	for _, s := range []string{"glm.json", "/login"} {
		if !strings.Contains(out, s) {
			t.Fatalf("output missing %q:\n%s", s, out)
		}
	}
}

// Nothing that identifies src — the login, its transcripts, its history — is
// copied: the clone logs in on its first run.
func TestDuplicateNeverCopiesIdentity(t *testing.T) {
	home, _, _ := dupFixture(t)
	if _, err := runDuplicate(t, "claude", "work", "work2"); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	for _, name := range []string{".credentials.json", "projects", "history.jsonl"} {
		if _, err := os.Lstat(filepath.Join(home, ".claude-work2", name)); !os.IsNotExist(err) {
			t.Errorf("%s reached the clone (err=%v)", name, err)
		}
	}
}

// contentTreeOf lists every path under root with its type and, for a file, a hash of
// its bytes, so a refusal can be shown to have written nothing anywhere under
// the hermetic HOME: no path added or removed, no file changed.
func contentTreeOf(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		entry := p + " " + d.Type().String()
		if d.Type().IsRegular() {
			body, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			entry += fmt.Sprintf(" %x", sha256.Sum256(body))
		}
		out = append(out, entry)
		return nil
	})
	slices.Sort(out)
	return out
}

// contentTreeOf sees a changed file, not just an added or removed path: a refusal
// that rewrote a file in place must not pass for one that wrote nothing.
func TestContentTreeOfSeesChangedFile(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "f")
	if err := os.WriteFile(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := contentTreeOf(t, root)
	if err := os.WriteFile(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if slices.Equal(before, contentTreeOf(t, root)) {
		t.Fatal("contentTreeOf did not see the file's bytes change")
	}
}

// Every refusal is decided before the first write: the config file and the
// disk are exactly as they were, and the error says what to do.
func TestDuplicateRefusals(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		setup func(t *testing.T, home, cfgPath string)
		want  []string
	}{
		{name: "unknown src", args: []string{"claude", "nope", "work2"},
			want: []string{`unknown account "nope"`, "claude/work"}},
		{name: "unknown provider", args: []string{"mytool", "work", "work2"},
			want: []string{`unknown provider "mytool"`, "claude/work"}},
		{name: "taken name", args: []string{"claude", "work", "taken"},
			want: []string{`"taken" already exists`}},
		{name: "invalid name", args: []string{"claude", "work", "../evil"},
			want: []string{"invalid account name"}},
		{name: "src equals new", args: []string{"claude", "work", "work"},
			want: []string{"already is work", "pick a new name"}},
		{name: "non-empty target dir", args: []string{"claude", "work", "work2", "--dir", "~/busy"},
			setup: func(t *testing.T, home, _ string) {
				if err := os.MkdirAll(filepath.Join(home, "busy"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, "busy", "x"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"~/busy is not empty", "--dir"}},
		{name: "target is src dir", args: []string{"claude", "work", "work2", "--dir", "~/.claude-work"},
			want: []string{"is work's own dir", "--dir"}},
		{name: "missing src settings file", args: []string{"claude", "work", "work2"},
			setup: func(t *testing.T, home, _ string) {
				if err := os.Remove(filepath.Join(home, ".claude-work", "glm.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"settings file ~/.claude-work/glm.json is missing", "restore it"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, cfgPath, _ := dupFixture(t)
			if tc.setup != nil {
				tc.setup(t, home, cfgPath)
			}
			cfgBefore, _ := os.ReadFile(cfgPath)
			diskBefore := contentTreeOf(t, home)

			_, err := runDuplicate(t, tc.args...)
			if err == nil {
				t.Fatal("duplicate succeeded, want a refusal")
			}
			for _, s := range tc.want {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q does not say %q", err, s)
				}
			}
			if cfgAfter, _ := os.ReadFile(cfgPath); !bytes.Equal(cfgBefore, cfgAfter) {
				t.Errorf("config changed:\n%s\n---\n%s", cfgBefore, cfgAfter)
			}
			if diskAfter := contentTreeOf(t, home); !slices.Equal(diskBefore, diskAfter) {
				t.Errorf("disk changed:\nbefore %v\nafter  %v", diskBefore, diskAfter)
			}
		})
	}
}

// A failure after the dir was created removes what this command made — the dir
// and the settings copy — and nothing else. A dir that already existed (empty)
// stays.
func TestDuplicateFailureRemovesOnlyWhatItCreated(t *testing.T) {
	for _, preexisting := range []bool{false, true} {
		home, cfgPath, _ := dupFixture(t)
		target := filepath.Join(home, ".claude-work2")
		if preexisting {
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		// The registration itself fails: the config dir refuses its lock file.
		cfgDir := filepath.Dir(cfgPath)
		if err := os.Chmod(cfgDir, 0o500); err != nil {
			t.Fatal(err)
		}
		_, err := runDuplicate(t, "claude", "work", "work2")
		_ = os.Chmod(cfgDir, 0o755)
		if err == nil {
			t.Fatal("duplicate succeeded with an unwritable config dir")
		}
		entries, statErr := os.ReadDir(target)
		switch {
		case preexisting && (statErr != nil || len(entries) != 0):
			t.Errorf("pre-existing dir: entries=%v err=%v, want it kept and empty", entries, statErr)
		case !preexisting && !os.IsNotExist(statErr):
			t.Errorf("created dir left behind: entries=%v err=%v", entries, statErr)
		}
	}
}

// The settings copy never overwrites: a file already at the target (a race
// with another writer after the empty-dir check) is refused and kept as it was.
func TestWriteNewNeverOverwrites(t *testing.T) {
	p := filepath.Join(t.TempDir(), "glm.json")
	if err := os.WriteFile(p, []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeNew(p, []byte("ours")); err == nil {
		t.Fatal("writeNew overwrote an existing file")
	}
	if got, _ := os.ReadFile(p); string(got) != "theirs" {
		t.Fatalf("existing file now %q, want it untouched", got)
	}
}

// A target whose parents are missing too: MkdirAll makes every one of them, so
// a failure after it removes the whole chain this command made — leaf first,
// each only if empty — and stops at the first dir that was already there.
func TestDuplicateFailureRemovesCreatedParents(t *testing.T) {
	home, cfgPath, _ := dupFixture(t)
	cfgDir := filepath.Dir(cfgPath)
	if err := os.Chmod(cfgDir, 0o500); err != nil {
		t.Fatal(err)
	}
	_, err := runDuplicate(t, "claude", "work", "work2", "--dir", "~/new/a/b/c")
	_ = os.Chmod(cfgDir, 0o755)
	if err == nil {
		t.Fatal("duplicate succeeded with an unwritable config dir")
	}
	if _, err := os.Lstat(filepath.Join(home, "new")); !os.IsNotExist(err) {
		t.Fatalf("created parents left behind under ~/new (err=%v): %v", err, contentTreeOf(t, filepath.Join(home, "new")))
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("the existing ancestor went too: %v", err)
	}
}
