package share

import (
	"os"
	"path/filepath"
	"testing"
)

var entries = []string{"skills", "agents", "CLAUDE.md"}

// base builds a source dir holding every entry, plus an extra that is not shared.
func base(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustDir(t, filepath.Join(dir, "skills"))
	mustDir(t, filepath.Join(dir, "agents"))
	mustFile(t, filepath.Join(dir, "CLAUDE.md"), "rules")
	mustFile(t, filepath.Join(dir, "history.jsonl"), "private")
	return dir
}

func mustDir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLinkCreatesSymlinks(t *testing.T) {
	b, acct := base(t), t.TempDir()
	res, err := Link(b, acct, entries, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Count(Linked); n != len(entries) {
		t.Fatalf("linked %d of %d: %+v", n, len(entries), res.Entries)
	}
	for _, name := range entries {
		target, err := os.Readlink(filepath.Join(acct, name))
		if err != nil {
			t.Fatalf("%s is not a symlink: %v", name, err)
		}
		if target != filepath.Join(b, name) {
			t.Fatalf("%s points at %s", name, target)
		}
	}
	// Per-account state must NOT be shared.
	if _, err := os.Lstat(filepath.Join(acct, "history.jsonl")); err == nil {
		t.Fatal("history.jsonl was shared — only authored assets may be")
	}
}

func TestLinkIsIdempotent(t *testing.T) {
	b, acct := base(t), t.TempDir()
	if _, err := Link(b, acct, entries, false); err != nil {
		t.Fatal(err)
	}
	res, err := Link(b, acct, entries, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Count(Already); n != len(entries) {
		t.Fatalf("second run relinked instead of no-op: %+v", res.Entries)
	}
}

func TestLinkNeverClobbersWithoutReplace(t *testing.T) {
	b, acct := base(t), t.TempDir()
	mine := filepath.Join(acct, "CLAUDE.md")
	mustFile(t, mine, "mine")

	res, err := Link(b, acct, entries, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(mine); err != nil || string(got) != "mine" {
		t.Fatalf("own file was destroyed: %q %v", got, err)
	}
	if c := res.Conflicts(); len(c) != 1 || c[0].Name != "CLAUDE.md" {
		t.Fatalf("conflict not reported: %+v", res.Entries)
	}
}

func TestReplaceBacksUpFirst(t *testing.T) {
	b, acct := base(t), t.TempDir()
	mustFile(t, filepath.Join(acct, "CLAUDE.md"), "mine")

	if _, err := Link(b, acct, entries, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(filepath.Join(acct, "CLAUDE.md")); err != nil {
		t.Fatalf("replace did not link: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(acct, "CLAUDE.md.aiacc-bak"))
	if err != nil || string(got) != "mine" {
		t.Fatalf("replace lost the original: %q %v", got, err)
	}
}

func TestLinkIntoBaseIsNoOp(t *testing.T) {
	b := base(t)
	res, err := Link(b, b, entries, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 0 {
		t.Fatalf("base profile was linked into itself: %+v", res.Entries)
	}
	if got, err := os.ReadFile(filepath.Join(b, "CLAUDE.md")); err != nil || string(got) != "rules" {
		t.Fatalf("base assets damaged: %q %v", got, err)
	}
}

func TestMissingSourceIsSkipped(t *testing.T) {
	b, acct := t.TempDir(), t.TempDir()
	res, err := Link(b, acct, entries, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Count(Absent); n != len(entries) {
		t.Fatalf("expected all absent: %+v", res.Entries)
	}
	if _, err := os.Lstat(filepath.Join(acct, "skills")); err == nil {
		t.Fatal("linked a source that does not exist")
	}
}

// A shared symlink outlives the directory it points at when the CLI renames a
// config surface. Before, Link stat'ed the source, found nothing, and reported
// "not in base" — the dead link in the profile was never mentioned.
func TestLinkReportsDanglingWhenSourceVanished(t *testing.T) {
	b, acct := base(t), t.TempDir()
	if _, err := Link(b, acct, entries, false); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(b, "skills")); err != nil {
		t.Fatal(err)
	}

	res, err := Link(b, acct, entries, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := status(res, "skills"); got != Dangling {
		t.Fatalf("skills = %q, want %q — a dead link must not read as %q", got, Dangling, Absent)
	}
}

func TestCheckIsReadOnly(t *testing.T) {
	b, acct := base(t), t.TempDir()

	res := Check(b, acct, entries)
	if n := res.Count(Missing); n != len(entries) {
		t.Fatalf("missing = %d of %d: %+v", n, len(entries), res.Entries)
	}
	for _, name := range entries {
		if _, err := os.Lstat(filepath.Join(acct, name)); err == nil {
			t.Fatalf("Check created %s — it must change nothing", name)
		}
	}
}

func TestCheckReportsLiveAndDeadLinks(t *testing.T) {
	b, acct := base(t), t.TempDir()
	if _, err := Link(b, acct, entries, false); err != nil {
		t.Fatal(err)
	}
	if res := Check(b, acct, entries); res.Count(Already) != len(entries) {
		t.Fatalf("want all live: %+v", res.Entries)
	}

	if err := os.RemoveAll(filepath.Join(b, "agents")); err != nil {
		t.Fatal(err)
	}
	res := Check(b, acct, entries)
	if got := status(res, "agents"); got != Dangling {
		t.Fatalf("agents = %q, want %q", got, Dangling)
	}
	if got := status(res, "skills"); got != Already {
		t.Fatalf("skills = %q, want %q — one dead link must not taint the rest", got, Already)
	}
}

// Prune is the only destructive path here, so it must be surgical: dead links
// go, everything else — including a link the user made by hand to a live target
// and a real file of their own — stays.
func TestPruneRemovesOnlyDeadLinks(t *testing.T) {
	b, acct := base(t), t.TempDir()
	if _, err := Link(b, acct, entries, false); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(b, "skills")); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(acct, "own.md")
	mustFile(t, own, "mine")

	res := Prune(acct, append(entries, "own.md"))
	if n := res.Count(Pruned); n != 1 {
		t.Fatalf("pruned %d, want exactly the one dead link: %+v", n, res.Entries)
	}
	if _, err := os.Lstat(filepath.Join(acct, "skills")); err == nil {
		t.Error("the dead link is still there")
	}
	for _, keep := range []string{"agents", "CLAUDE.md", "own.md"} {
		if _, err := os.Lstat(filepath.Join(acct, keep)); err != nil {
			t.Errorf("Prune removed %s, which was not dead: %v", keep, err)
		}
	}
}

// status is the recorded Status for one entry name.
func status(r Result, name string) Status {
	for _, e := range r.Entries {
		if e.Name == name {
			return e.Status
		}
	}
	return ""
}
