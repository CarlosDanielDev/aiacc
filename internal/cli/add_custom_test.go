package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
)

// readSkeletonEnv reads a generated settings file's env block.
func readSkeletonEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("skeleton does not parse: %v\n%s", err, raw)
	}
	return doc.Env
}

// assertNothingAdded fails when a refused add left an account or a file behind.
func assertNothingAdded(t *testing.T, cfgPath, dir string) {
	t.Helper()
	for _, name := range []string{"custom.json", "glm.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("refused add wrote %s", name)
		}
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Providers["claude"].Accounts) != 0 {
		t.Errorf("refused add registered %+v", c.Providers["claude"].Accounts)
	}
}

// TestAddCustomEndpointFromFlags: --settings custom with --base-url and
// --model writes the skeleton with those values and the placeholder token,
// mode 0600, registers it, and says where the key goes.
func TestAddCustomEndpointFromFlags(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	var out bytes.Buffer
	add := newAddCmd()
	add.SetOut(&out)
	add.SetArgs([]string{"claude", "claude-ds", "--dir", dir, "--settings", "custom",
		"--base-url", "https://api.example.com/anthropic", "--model", "m1"})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}
	skeleton := filepath.Join(dir, "custom.json")
	info, err := os.Stat(skeleton)
	if err != nil {
		t.Fatalf("skeleton not written: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("skeleton mode = %o, want 600", mode)
	}
	env := readSkeletonEnv(t, skeleton)
	for k, v := range map[string]string{
		"ANTHROPIC_BASE_URL":                 "https://api.example.com/anthropic",
		"ANTHROPIC_AUTH_TOKEN":               provider.PlaceholderToken,
		"ANTHROPIC_MODEL":                    "m1",
		"ANTHROPIC_CUSTOM_MODEL_OPTION":      "m1",
		"ANTHROPIC_CUSTOM_MODEL_OPTION_NAME": "m1",
	} {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers["claude"].Accounts["claude-ds"].Settings; got != skeleton {
		t.Errorf("settings = %q, want %q", got, skeleton)
	}
	if !strings.Contains(out.String(), skeleton) {
		t.Errorf("output does not name the file to paste the key into: %q", out.String())
	}
}

// TestAddCustomEndpointModelName: --model-name sets the display name.
func TestAddCustomEndpointModelName(t *testing.T) {
	withTempConfig(t)
	dir := t.TempDir()
	add := newAddCmd()
	add.SetOut(&bytes.Buffer{})
	add.SetArgs([]string{"claude", "ds", "--dir", dir, "--settings", "custom",
		"--base-url", "http://localhost:8080/", "--model", "deepseek-chat", "--model-name", "DeepSeek"})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}
	env := readSkeletonEnv(t, filepath.Join(dir, "custom.json"))
	if env["ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"] != "DeepSeek" || env["ANTHROPIC_BASE_URL"] != "http://localhost:8080" {
		t.Errorf("env = %v", env)
	}
}

// TestAddEndpointFlagsRefused: the endpoint flags are refused, not ignored,
// with any settings value but custom; custom without both --base-url and
// --model is refused naming what to add; a bad URL is refused. Every refusal
// leaves nothing on disk.
func TestAddEndpointFlagsRefused(t *testing.T) {
	cases := []struct {
		name    string
		flags   []string
		want    []string
		notWant string
	}{
		{name: "base-url with glm", flags: []string{"--settings", "glm", "--base-url", "https://h"},
			want: []string{"--base-url", "--settings custom"}},
		{name: "model with a path", flags: []string{"--settings", "./s.json", "--model", "m1"},
			want: []string{"--model", "--settings custom"}},
		{name: "model-name with no settings", flags: []string{"--model-name", "N"},
			want: []string{"--model-name", "--settings custom"}},
		{name: "custom alone", flags: []string{"--settings", "custom"},
			want: []string{"--base-url", "--model"}},
		{name: "custom without model", flags: []string{"--settings", "custom", "--base-url", "https://h"},
			want: []string{"--model"}, notWant: "--base-url"},
		{name: "custom without base-url", flags: []string{"--settings", "custom", "--model", "m1"},
			want: []string{"--base-url"}, notWant: "--model "},
		{name: "custom with plain http", flags: []string{"--settings", "custom", "--base-url", "http://example.com", "--model", "m1"},
			want: []string{"https"}},
		{name: "custom with a spaced model", flags: []string{"--settings", "custom", "--base-url", "https://h", "--model", "m 1"},
			want: []string{"whitespace"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := withTempConfig(t)
			parent := t.TempDir()
			dir := filepath.Join(parent, "acct")
			add := newAddCmd()
			add.SetOut(&bytes.Buffer{})
			add.SetArgs(append([]string{"claude", "foo", "--dir", dir}, tc.flags...))
			err := add.Execute()
			if err == nil {
				t.Fatalf("add %v was accepted", tc.flags)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal missing %q: %v", w, err)
				}
			}
			if tc.notWant != "" && strings.Contains(err.Error(), tc.notWant) {
				t.Errorf("refusal names %q, which was given: %v", tc.notWant, err)
			}
			if _, statErr := os.Stat(dir); statErr == nil {
				t.Errorf("refused add created the account dir")
			}
			assertNothingAdded(t, path, dir)
		})
	}
}

// TestAddSettingsDirForProviderWithoutFlag: a directory given as --settings is
// refused as a directory before the provider's missing settings flag is, the
// order the add path had before the endpoint flags existed.
func TestAddSettingsDirForProviderWithoutFlag(t *testing.T) {
	path := withTempConfig(t)
	dir := filepath.Join(t.TempDir(), "acct")
	add := newAddCmd()
	add.SetOut(&bytes.Buffer{})
	add.SetArgs([]string{"codex", "foo", "--dir", dir, "--settings", t.TempDir()})
	err := add.Execute()
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("want the directory refusal first, got %v", err)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Errorf("refused add created the account dir")
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Providers) != 0 {
		t.Errorf("refused add registered %+v", c.Providers)
	}
}
