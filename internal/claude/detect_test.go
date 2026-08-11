package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectInsideDir(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".claude.json"),
		`{"numStartups":3,"oauthAccount":{"emailAddress":"a@b.com","displayName":"A","organizationName":"Org"}}`)
	got := Detect(dir)
	if !got.LoggedIn || got.Email != "a@b.com" || got.DisplayName != "A" || got.Organization != "Org" {
		t.Fatalf("Detect = %+v", got)
	}
}

func TestDetectSiblingJSON(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, ".claude")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Default layout: ~/.claude's config lives at ~/.claude.json (sibling).
	write(t, dir+".json", `{"oauthAccount":{"emailAddress":"s@b.com"}}`)
	if got := Detect(dir); got.Email != "s@b.com" {
		t.Fatalf("Detect sibling = %+v", got)
	}
}

func TestDetectNotLoggedIn(t *testing.T) {
	if Detect(t.TempDir()).LoggedIn {
		t.Fatal("empty dir should be not-logged-in")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".claude.json"), `{"numStartups":3}`) // no oauthAccount
	if Detect(dir).LoggedIn {
		t.Fatal("config without oauthAccount should be not-logged-in")
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
