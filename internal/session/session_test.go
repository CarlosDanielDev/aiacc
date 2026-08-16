package session

import (
	"os"
	"path/filepath"
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
