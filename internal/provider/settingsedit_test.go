package provider

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sentinelToken stands in for a real API key. No error, frame or assertion
// message may ever contain it.
const sentinelToken = "sk-SENTINEL-123"

// settingsFixture writes body to a fresh settings.json at mode and returns its
// path. The mode is applied after the write so the umask cannot change it.
func settingsFixture(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func readJSON(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("file is not valid JSON after edit: %v", err)
	}
	return doc
}

func envOf(t *testing.T, doc map[string]json.RawMessage) map[string]string {
	t.Helper()
	var env map[string]string
	if err := json.Unmarshal(doc["env"], &env); err != nil {
		t.Fatalf("env block: %v", err)
	}
	return env
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestReadSettingsEnvNeverReturnsToken(t *testing.T) {
	path := settingsFixture(t, `{"env":{"ANTHROPIC_AUTH_TOKEN":"`+sentinelToken+`","ANTHROPIC_BASE_URL":"https://glm.example/api","ANTHROPIC_MODEL":"glm-4.6"}}`, 0o600)
	got, err := ReadSettingsEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != "https://glm.example/api" || got.Model != "glm-4.6" || !got.HasToken {
		t.Fatalf("read %+v, want base, model and HasToken", got)
	}
	// The read value is what the edit screen gets. It must not carry the key.
	blob, _ := json.Marshal(got)
	if strings.Contains(string(blob), sentinelToken) {
		t.Fatalf("SettingsEnv leaks the token: %s", blob)
	}
}

func TestEditSettingsPreservesOtherKeys(t *testing.T) {
	path := settingsFixture(t, `{
  "permissions": {"allow": ["Bash(ls)"]},
  "model": "opus",
  "env": {"ANTHROPIC_BASE_URL": "https://old.example", "ANTHROPIC_AUTH_TOKEN": "old-token", "FOO": "keep"}
}`, 0o600)
	if err := EditSettings(path, SettingsEdit{Token: "new-token", BaseURL: "https://new.example"}); err != nil {
		t.Fatal(err)
	}
	doc := readJSON(t, path)
	env := envOf(t, doc)
	if env["ANTHROPIC_AUTH_TOKEN"] != "new-token" || env["ANTHROPIC_BASE_URL"] != "https://new.example" {
		t.Fatalf("owned keys not updated: %v", env)
	}
	if env["FOO"] != "keep" {
		t.Fatalf("unrelated env key lost: %v", env)
	}
	if _, ok := env["ANTHROPIC_MODEL"]; ok {
		t.Fatalf("empty model must not be invented: %v", env)
	}
	if string(doc["model"]) != `"opus"` {
		t.Fatalf("top-level key changed: %s", doc["model"])
	}
	if !strings.Contains(string(doc["permissions"]), `"Bash(ls)"`) {
		t.Fatalf("permissions block lost: %s", doc["permissions"])
	}
}

func TestEditSettingsEmptyTokenKeepsExisting(t *testing.T) {
	path := settingsFixture(t, `{"env":{"ANTHROPIC_AUTH_TOKEN":"old-token","ANTHROPIC_BASE_URL":"https://old.example"}}`, 0o600)
	if err := EditSettings(path, SettingsEdit{Token: "", BaseURL: "https://new.example"}); err != nil {
		t.Fatal(err)
	}
	env := envOf(t, readJSON(t, path))
	if env["ANTHROPIC_AUTH_TOKEN"] != "old-token" {
		t.Fatalf("empty token must keep the existing one, got %q", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if env["ANTHROPIC_BASE_URL"] != "https://new.example" {
		t.Fatalf("base URL not written: %v", env)
	}
}

func TestEditSettingsWritesModelWhenSet(t *testing.T) {
	path := settingsFixture(t, `{"env":{"ANTHROPIC_BASE_URL":"https://x"}}`, 0o600)
	if err := EditSettings(path, SettingsEdit{Model: "glm-4.6"}); err != nil {
		t.Fatal(err)
	}
	if got := envOf(t, readJSON(t, path))["ANTHROPIC_MODEL"]; got != "glm-4.6" {
		t.Fatalf("model = %q, want glm-4.6", got)
	}
}

func TestEditSettingsCreatesEnvWhenAbsent(t *testing.T) {
	path := settingsFixture(t, `{"permissions":{}}`, 0o600)
	if err := EditSettings(path, SettingsEdit{Token: sentinelToken}); err != nil {
		t.Fatal(err)
	}
	if got := envOf(t, readJSON(t, path))["ANTHROPIC_AUTH_TOKEN"]; got != sentinelToken {
		t.Fatalf("token not written into a new env block")
	}
}

// TestEditSettingsModeStaysPrivate: the result is never wider than 0600, and a
// stricter existing mode is kept. Mutation check: dropping the chmod leaves the
// temp file's own mode in place and this fails for the 0644 case.
func TestEditSettingsModeStaysPrivate(t *testing.T) {
	cases := []struct {
		name     string
		before   os.FileMode
		wantMode os.FileMode
	}{
		{"widened file is narrowed", 0o644, 0o600},
		{"group-readable file is narrowed", 0o640, 0o600},
		{"private file stays private", 0o600, 0o600},
		{"read-only file stays read-only", 0o400, 0o400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := settingsFixture(t, `{"env":{}}`, tc.before)
			if err := EditSettings(path, SettingsEdit{Token: sentinelToken}); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != tc.wantMode {
				t.Fatalf("mode = %o, want %o", got, tc.wantMode)
			}
		})
	}
}

// TestEditSettingsRefusesWithoutWriting: every refusal leaves the file and its
// directory exactly as they were, and the error never carries the token.
func TestEditSettingsRefusesWithoutWriting(t *testing.T) {
	const badJSON = `{"env":{"ANTHROPIC_AUTH_TOKEN":"` + sentinelToken + `",,,`
	cases := []struct {
		name string
		body string // file content, unless dir or miss says otherwise
		dir  bool   // the path is a directory
		miss bool   // the path does not exist
		want string // a fragment the refusal must say
	}{
		{name: "missing file", miss: true, want: "is missing"},
		{name: "directory", dir: true, want: "is a directory"},
		{name: "invalid JSON", body: badJSON, want: "is not valid JSON"},
		{name: "JSON null", body: "null", want: "is not valid JSON"},
		{name: "env not an object", body: `{"env":"` + sentinelToken + `"}`, want: "env block"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.json")
			switch {
			case tc.dir:
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			case tc.miss:
			default:
				if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := dirNames(t, dir)
			var content []byte
			if !tc.dir && !tc.miss {
				content, _ = os.ReadFile(path)
			}

			err := EditSettings(path, SettingsEdit{Token: sentinelToken, BaseURL: "https://x"})
			if err == nil {
				t.Fatal("edit accepted a file it must refuse")
			}
			var se SettingsError
			if !errors.As(err, &se) || se.Path != path {
				t.Fatalf("refusal is not a SettingsError naming the file: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not say %q", err, tc.want)
			}
			if strings.Contains(err.Error(), sentinelToken) {
				t.Fatalf("error leaks the token: %q", err)
			}
			if got := dirNames(t, dir); strings.Join(got, ",") != strings.Join(before, ",") {
				t.Fatalf("directory changed: before %v, after %v", before, got)
			}
			if !tc.dir && !tc.miss {
				after, _ := os.ReadFile(path)
				if string(after) != string(content) {
					t.Fatalf("file was rewritten despite the refusal")
				}
			}
		})
	}
}

// TestEditSettingsFollowsSymlink: a settings path that is a symlink (aiacc link
// shares settings this way) is edited at its target and stays a symlink.
func TestEditSettingsFollowsSymlink(t *testing.T) {
	target := settingsFixture(t, `{"env":{"ANTHROPIC_BASE_URL":"https://old"}}`, 0o600)
	link := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := EditSettings(link, SettingsEdit{BaseURL: "https://new"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file")
	}
	if got := envOf(t, readJSON(t, target))["ANTHROPIC_BASE_URL"]; got != "https://new" {
		t.Fatalf("target not updated: %q", got)
	}
}

// TestAbortSettingsWritesCleansTempAndRefusesNewWrites: a signal that lands
// between creating the temp file and renaming it must not leave the temp file
// (it holds the new token) behind, and must stop the write from completing.
// The hook fires in exactly that window.
func TestAbortSettingsWritesCleansTempAndRefusesNewWrites(t *testing.T) {
	const body = `{"env":{"ANTHROPIC_BASE_URL":"https://old"}}`
	path := settingsFixture(t, body, 0o600)
	dir := filepath.Dir(path)

	old := SettingsWriteWindow
	t.Cleanup(func() { SettingsWriteWindow = old; resetAbortedForTest() })
	SettingsWriteWindow = func() {
		if n := tempCount(t, dir); n != 1 {
			t.Fatalf("in the write window there should be one temp file, found %d", n)
		}
		AbortSettingsWrites()
	}

	err := EditSettings(path, SettingsEdit{Token: sentinelToken})
	if err == nil {
		t.Fatal("write completed after an abort")
	}
	if strings.Contains(err.Error(), sentinelToken) {
		t.Fatalf("error leaks the token: %q", err)
	}
	if n := tempCount(t, dir); n != 0 {
		t.Fatalf("%d temp file(s) left behind after abort", n)
	}
	if raw, _ := os.ReadFile(path); string(raw) != body {
		t.Fatal("aborted write changed the settings file")
	}

	SettingsWriteWindow = func() {}
	if err := EditSettings(path, SettingsEdit{Token: sentinelToken}); err == nil {
		t.Fatal("a write started after the abort was not refused")
	}
}

// tempCount counts the settings temp files in dir.
func tempCount(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	for _, name := range dirNames(t, dir) {
		if strings.HasPrefix(name, ".settings-") && strings.HasSuffix(name, ".tmp") {
			n++
		}
	}
	return n
}

// resetAbortedForTest reopens the write registry after an abort, so later tests
// in the package can write again.
func resetAbortedForTest() {
	writes.Lock()
	defer writes.Unlock()
	writes.aborted = false
}
