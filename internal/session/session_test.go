package session

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seed writes a session transcript under configDir/projects/projDir/id.jsonl.
func seed(t *testing.T, configDir, projDir, id string, lines []string) string {
	t.Helper()
	dir := filepath.Join(configDir, "projects", projDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestListNewestFirst(t *testing.T) {
	cfg := t.TempDir()
	p1 := seed(t, cfg, "-proj-a", "old", []string{`{"type":"user","cwd":"/proj/a"}`})
	p2 := seed(t, cfg, "-proj-a", "new", []string{`{"type":"user","cwd":"/proj/a"}`})
	// Make "new" clearly newer.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p1, old, old); err != nil {
		t.Fatal(err)
	}
	_ = p2

	list, err := List(cfg)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d sessions, want 2", len(list))
	}
	if list[0].ID != "new" {
		t.Fatalf("newest first failed: got %q", list[0].ID)
	}
	if list[0].ProjectDir != "-proj-a" {
		t.Fatalf("project dir = %q, want -proj-a", list[0].ProjectDir)
	}
}

func TestListMissingProjectsIsEmpty(t *testing.T) {
	list, err := List(t.TempDir()) // no projects/ dir
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("want empty, got %d", len(list))
	}
}

func TestReadMeta(t *testing.T) {
	cfg := t.TempDir()
	p := seed(t, cfg, "-proj-a", "s1", []string{
		`{"type":"summary"}`,
		`{"type":"user","cwd":"/Users/x/proj","gitBranch":"main","lastPrompt":"fix the parser"}`,
		`{"type":"assistant","cwd":"/Users/x/proj","customTitle":"Parser fix"}`,
	})
	m, err := ReadMeta(p)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if m.Cwd != "/Users/x/proj" {
		t.Errorf("cwd = %q", m.Cwd)
	}
	if m.GitBranch != "main" {
		t.Errorf("branch = %q", m.GitBranch)
	}
	if m.Title != "Parser fix" { // customTitle wins over lastPrompt
		t.Errorf("title = %q, want Parser fix", m.Title)
	}
	if m.Messages != 2 {
		t.Errorf("messages = %d, want 2", m.Messages)
	}
}

func TestCopyPreservesProjectDir(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	seed(t, from, "-proj-a", "s1", []string{`{"type":"user","cwd":"/proj/a"}`})

	list, _ := List(from)
	dest, err := Copy(list[0], to)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	want := filepath.Join(to, "projects", "-proj-a", "s1.jsonl")
	if dest != want {
		t.Fatalf("dest = %q, want %q", dest, want)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("copied file missing: %v", err)
	}
	// And the target account now lists it.
	toList, _ := List(to)
	if len(toList) != 1 || toList[0].ID != "s1" {
		t.Fatalf("target does not see the session: %+v", toList)
	}
}

// TestCopyRefusesSelfOverwrite pins the data-loss guard: handing a session to
// the account it already lives in makes the destination resolve to the source
// file, and truncating that before reading used to zero the transcript. Copy
// must refuse and leave the source byte-identical.
func TestCopyRefusesSelfOverwrite(t *testing.T) {
	cfg := t.TempDir()
	p := seed(t, cfg, "-proj-a", "s1", []string{`{"type":"user","cwd":"/proj/a"}`})
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	list, _ := List(cfg)
	if _, err := Copy(list[0], cfg); err == nil {
		t.Fatal("expected Copy into the source's own config dir to be refused")
	}

	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("transcript clobbered: want %q, got %q", before, after)
	}
}

func TestFind(t *testing.T) {
	cfg := t.TempDir()
	seed(t, cfg, "-proj-a", "wanted", []string{`{"type":"user"}`})
	if s, err := Find(cfg, "wanted"); err != nil || s.ID != "wanted" {
		t.Fatalf("Find hit: %+v %v", s, err)
	}
	if _, err := Find(cfg, "nope"); err == nil {
		t.Fatal("Find should error on a missing id")
	}
}

// TestCopyRefusesSymlinkAndHardlinkToSource pins the aliasing guard: a
// destination whose path string differs from the source but resolves to the
// same file (a symlink, or a hardlink) must be refused too, because O_TRUNC on
// it would still zero the transcript. Both cases live in t.TempDir().
func TestCopyRefusesSymlinkAndHardlinkToSource(t *testing.T) {
	cfg := t.TempDir()
	p := seed(t, cfg, "-proj-a", "s1", []string{`{"type":"user","cwd":"/proj/a"}`})
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := List(cfg)

	alias := func(t *testing.T, link func(old, new string) error) {
		other := t.TempDir()
		destDir := filepath.Join(other, "projects", "-proj-a")
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(destDir, "s1.jsonl")
		if err := link(p, dest); err != nil {
			t.Skipf("OS refuses to create the link: %v", err)
		}
		if _, err := Copy(list[0], other); err == nil {
			t.Fatal("expected Copy onto a destination aliasing the source to be refused")
		}
		after, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("source transcript clobbered: want %q, got %q", before, after)
		}
	}

	t.Run("symlink", func(t *testing.T) { alias(t, os.Symlink) })
	t.Run("hardlink", func(t *testing.T) { alias(t, os.Link) })
}

// TestCopyFailsClosedWhenDestStatFails pins fail-closed behaviour: when the
// destination cannot be stat'ed for a reason other than "does not exist", Copy
// must refuse and leave the destination untouched, not fall through to the
// truncating open. The destination's directory is made unsearchable, so the
// stat fails with EACCES. The test's "can't stat" assertion is what tells the
// guard apart from the open failing with the same EACCES.
func TestCopyFailsClosedWhenDestStatFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions; cannot force a stat error")
	}
	cfg := t.TempDir()
	p := seed(t, cfg, "-proj-a", "s1", []string{`{"type":"user","cwd":"/proj/a"}`})
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := List(cfg)

	other := t.TempDir()
	destDir := filepath.Join(other, "projects", "-proj-a")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(destDir, "s1.jsonl")
	const keep = `{"type":"user","keep":"dest"}` + "\n"
	if err := os.WriteFile(dest, []byte(keep), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destDir, 0o000); err != nil {
		t.Skipf("chmod unsupported: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(destDir, 0o755) })

	_, copyErr := Copy(list[0], other)
	if err := os.Chmod(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if copyErr == nil {
		t.Fatal("expected Copy to refuse when the destination cannot be stat'ed")
	}
	if !strings.Contains(copyErr.Error(), "can't stat destination") || !errors.Is(copyErr, fs.ErrPermission) {
		t.Fatalf("want the stat guard's refusal wrapping EACCES, got: %v", copyErr)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != keep {
		t.Fatalf("destination was touched: want %q, got %q", keep, got)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("source transcript clobbered: want %q, got %q", before, after)
	}
}

// TestCopyReturnsDestinationCloseError pins the success-path close: a write
// that fails only at close must surface as an error naming the destination,
// not as a short transcript reported as handed off. closeDest is swapped for
// one that really closes the file (no fd leak) and then reports an injected
// error. The destination is left in place, since it may be a pre-existing
// session file.
func TestCopyReturnsDestinationCloseError(t *testing.T) {
	cfg := t.TempDir()
	seed(t, cfg, "-proj-a", "s1", []string{`{"type":"user","cwd":"/proj/a"}`})
	list, _ := List(cfg)
	other := t.TempDir()

	injected := errors.New("injected close failure")
	old := closeDest
	closeDest = func(f *os.File) error {
		_ = f.Close()
		return injected
	}
	t.Cleanup(func() { closeDest = old })

	dest, err := Copy(list[0], other)
	if !errors.Is(err, injected) {
		t.Fatalf("want Copy to return the close error, got: %v", err)
	}
	want := filepath.Join(other, "projects", "-proj-a", "s1.jsonl")
	if !strings.Contains(err.Error(), "closing destination "+want) {
		t.Fatalf("error should name the destination %s, got: %v", want, err)
	}
	if dest != "" {
		t.Fatalf("want no destination path on failure, got %q", dest)
	}
	if _, statErr := os.Stat(want); statErr != nil {
		t.Fatalf("destination must be left in place after a close error: %v", statErr)
	}
}

// The picker and handoff screens show the 8-char short id, so --session must
// take a prefix of it. An exact id still wins over a prefix that happens to
// match it, and a prefix that matches two sessions is refused by name.
func TestFindAcceptsUniquePrefix(t *testing.T) {
	cfg := t.TempDir()
	seed(t, cfg, "-proj-a", "abc123de-0000-4000-8000-000000000001", []string{`{"type":"user"}`})
	seed(t, cfg, "-proj-a", "fff999aa-0000-4000-8000-000000000002", []string{`{"type":"user"}`})
	s, err := Find(cfg, "abc123de")
	if err != nil || s.ID != "abc123de-0000-4000-8000-000000000001" {
		t.Fatalf("Find on unique prefix: %+v %v", s, err)
	}
}

func TestFindPrefersExactIDOverPrefix(t *testing.T) {
	cfg := t.TempDir()
	seed(t, cfg, "-proj-a", "abc", []string{`{"type":"user"}`})
	seed(t, cfg, "-proj-a", "abcd", []string{`{"type":"user"}`})
	if s, err := Find(cfg, "abc"); err != nil || s.ID != "abc" {
		t.Fatalf("exact id should win: %+v %v", s, err)
	}
}

func TestFindRefusesAmbiguousPrefix(t *testing.T) {
	cfg := t.TempDir()
	seed(t, cfg, "-proj-a", "abc123de-one", []string{`{"type":"user"}`})
	seed(t, cfg, "-proj-b", "abc123de-two", []string{`{"type":"user"}`})
	_, err := Find(cfg, "abc123de")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") ||
		!strings.Contains(err.Error(), "abc123de-one") || !strings.Contains(err.Error(), "abc123de-two") {
		t.Fatalf("want an ambiguity error naming both ids, got %v", err)
	}
}

func TestFindNoPrefixMatchIsNotFound(t *testing.T) {
	cfg := t.TempDir()
	seed(t, cfg, "-proj-a", "abc123de", []string{`{"type":"user"}`})
	if _, err := Find(cfg, "zzz"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want not-found for a prefix nothing starts with, got %v", err)
	}
}
