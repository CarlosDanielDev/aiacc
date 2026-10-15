package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
)

func withTempConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	old := configPath
	configPath = func() (string, error) { return p, nil }
	t.Cleanup(func() { configPath = old })
	return p
}

func TestAddThenListShowsAccount(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "work", "--dir", dir})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}

	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Providers["claude"].Accounts["work"].Dir != dir {
		t.Fatalf("account not saved: %+v", c.Providers["claude"])
	}

	list := newListCmd()
	var out bytes.Buffer
	list.SetOut(&out)
	if err := list.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "work") || !strings.Contains(out.String(), "claude") {
		t.Fatalf("list output missing account: %q", out.String())
	}
}

func TestRemoveDeletesAccount(t *testing.T) {
	withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "work", "--dir", dir})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}
	rm := newRemoveCmd()
	rm.SetArgs([]string{"claude", "work"})
	if err := rm.Execute(); err != nil {
		t.Fatalf("remove: %v", err)
	}

	path, _ := configPath()
	c, _ := config.Load(path)
	if _, ok := c.Providers["claude"].Accounts["work"]; ok {
		t.Fatalf("account still present after remove")
	}
}

// TestAddSettingsKeyword: --settings glm is an endpoint keyword, not a path —
// the skeleton is generated into the account dir (mode 0600) and the account
// registers with the generated path.
func TestAddSettingsKeyword(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "foo", "--dir", dir, "--settings", "glm"})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}

	skeleton := filepath.Join(dir, "glm.json")
	info, err := os.Stat(skeleton)
	if err != nil {
		t.Fatalf("skeleton not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("skeleton mode %v, want 0600", info.Mode().Perm())
	}
	body, err := os.ReadFile(skeleton)
	if err != nil {
		t.Fatalf("read skeleton: %v", err)
	}
	for _, want := range []string{"ANTHROPIC_BASE_URL", "https://api.z.ai/api/anthropic", provider.PlaceholderToken} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("skeleton missing %s:\n%s", want, body)
		}
	}

	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.Providers["claude"].Accounts["foo"].Settings; got != skeleton {
		t.Fatalf("settings = %q, want the generated path %q", got, skeleton)
	}
}

// TestAddSettingsPathUnchanged: a real path behaves exactly as before (no
// generation, no write), and a keyword whose target file already exists
// aborts the add with the path named, registering nothing.
func TestAddSettingsPathUnchanged(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	real := filepath.Join(dir, "my-endpoint.json")
	want := []byte("{\"env\":{}}\n")
	if err := os.WriteFile(real, want, 0o600); err != nil {
		t.Fatalf("write real settings: %v", err)
	}

	add := newAddCmd()
	add.SetArgs([]string{"claude", "foo", "--dir", dir, "--settings", real})
	if err := add.Execute(); err != nil {
		t.Fatalf("add with a real path: %v", err)
	}
	body, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("read real settings: %v", err)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("real settings file was rewritten:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "glm.json")); err == nil {
		t.Fatalf("a skeleton was generated for a path value")
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.Providers["claude"].Accounts["foo"].Settings; got != real {
		t.Fatalf("settings = %q, want the path verbatim %q", got, real)
	}

	target := filepath.Join(dir, "glm.json")
	if err := os.WriteFile(target, []byte("the user's own file\n"), 0o600); err != nil {
		t.Fatalf("write existing target: %v", err)
	}
	add.SetArgs([]string{"claude", "bar", "--dir", dir, "--settings", "glm"})
	err = add.Execute()
	if err == nil {
		t.Fatalf("add onto an existing glm.json succeeded")
	}
	if !strings.Contains(err.Error(), target) {
		t.Fatalf("error does not name the existing path %s: %v", target, err)
	}
	body, err = os.ReadFile(target)
	if err != nil || string(body) != "the user's own file\n" {
		t.Fatalf("existing glm.json was touched: %q, %v", body, err)
	}
	c, err = config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := c.Providers["claude"].Accounts["bar"]; ok {
		t.Fatalf("account registered despite the aborted add")
	}
}

// TestAddSettingsValueShape pins the path-vs-keyword boundary of the
// --settings value: a dot or a ~ reads as a path and registers verbatim with
// nothing generated, a bare word reads as a keyword attempt and — the
// registry being case-sensitive — an unknown one is refused with the valid
// keywords listed.
func TestAddSettingsValueShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		value    string
		wantPath string // registered verbatim, when set
		wantErr  string // refused with this substring, when set
	}{
		{name: "dot reads as a relative path", value: "settings.json", wantPath: "settings.json"},
		{name: "tilde reads as a home path", value: "~/e.json", wantPath: "~/e.json"},
		{name: "uppercase is an unknown keyword", value: "GLM", wantErr: "custom, glm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := withTempConfig(t)
			dir := t.TempDir()

			add := newAddCmd()
			add.SetArgs([]string{"claude", "foo", "--dir", dir, "--settings", tc.value})
			err := add.Execute()
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("--settings %s was accepted", tc.value)
				}
				for _, want := range []string{tc.value, tc.wantErr} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("error does not mention %q: %v", want, err)
					}
				}
			} else if err != nil {
				t.Fatalf("add: %v", err)
			}

			if tc.wantErr != "" {
				// A refusal lands before the create, so the dir is untouched
				// (a success row is populated by linkProfile's symlinks).
				if entries, err := os.ReadDir(dir); err != nil {
					t.Fatalf("read dir: %v", err)
				} else if len(entries) != 0 {
					t.Fatalf("the account dir was touched by the refusal: %v", entries)
				}
			} else if _, err := os.Stat(filepath.Join(dir, "glm.json")); err == nil {
				t.Fatalf("a skeleton was generated for the path value %q", tc.value)
			}
			c, err := config.Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			accounts := c.Providers["claude"].Accounts
			if tc.wantErr != "" {
				if len(accounts) != 0 {
					t.Fatalf("account registered despite the refusal: %+v", accounts)
				}
				return
			}
			if got := accounts["foo"].Settings; got != tc.wantPath {
				t.Fatalf("settings = %q, want %q verbatim", got, tc.wantPath)
			}
		})
	}
}

// TestAddSettingsUnknown: a bare word that is no endpoint keyword is refused
// with the valid ones listed (a typo must not silently register a bogus
// settings path), and custom points at the interactive add screen — the CLI
// has nowhere to ask for its base URL.
func TestAddSettingsUnknown(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "foo", "--dir", dir, "--settings", "kimi"})
	err := add.Execute()
	if err == nil {
		t.Fatalf("--settings kimi was accepted")
	}
	for _, want := range []string{"kimi", "glm", "custom"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("kimi error does not list valid keywords (missing %q): %v", want, err)
		}
	}

	add.SetArgs([]string{"claude", "bar", "--dir", dir, "--settings", "custom"})
	err = add.Execute()
	if err == nil {
		t.Fatalf("--settings custom was accepted")
	}
	if !strings.Contains(err.Error(), "aiacc add") {
		t.Fatalf("custom error does not point at the interactive add screen: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "custom.json")); err == nil {
		t.Fatalf("a custom skeleton was generated from the CLI")
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(c.Providers["claude"].Accounts) != 0 {
		t.Fatalf("accounts registered despite the refusals: %+v", c.Providers["claude"].Accounts)
	}
}
