package provider

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// readEnv reads a generated skeleton and returns its env block, failing the
// test when the file is missing or does not parse.
func readEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("written file does not parse: %v\n%s", err, raw)
	}
	return doc.Env
}

func TestEndpointResolve(t *testing.T) {
	glm, err := ResolveEndpoint("claude", "glm")
	if err != nil {
		t.Fatalf("ResolveEndpoint(claude, glm): %v", err)
	}
	if glm.BaseURL != "https://api.z.ai/api/anthropic" {
		t.Errorf("glm BaseURL = %q, want https://api.z.ai/api/anthropic", glm.BaseURL)
	}
	if glm.Model != "glm-5.3-flash[1m]" {
		t.Errorf("glm Model = %q, want glm-5.3-flash[1m]", glm.Model)
	}
	if glm.Filename != "glm.json" {
		t.Errorf("glm Filename = %q, want glm.json", glm.Filename)
	}
	custom, err := ResolveEndpoint("claude", "custom")
	if err != nil {
		t.Fatalf("ResolveEndpoint(claude, custom): %v", err)
	}
	if custom.BaseURL != "" {
		t.Errorf("custom BaseURL = %q, want empty (the user fills it in)", custom.BaseURL)
	}
}

// The skeleton is the whole product of this slice: a file the user opens in an
// editor, pastes their key into, and never sees aiacc touch again (ADR-0007).
func TestSkeletonWritesFreshFile(t *testing.T) {
	dir := t.TempDir()
	glm, err := ResolveEndpoint("claude", "glm")
	if err != nil {
		t.Fatalf("ResolveEndpoint(claude, glm): %v", err)
	}
	path := filepath.Join(dir, glm.Filename)
	if err := WriteSettingsSkeleton(glm, dir); err != nil {
		t.Fatalf("WriteSettingsSkeleton: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600 (the file is about to hold a key)", info.Mode().Perm())
	}
	want := map[string]string{
		"ANTHROPIC_BASE_URL":                 "https://api.z.ai/api/anthropic",
		"ANTHROPIC_AUTH_TOKEN":               PlaceholderToken,
		"ANTHROPIC_MODEL":                    "glm-5.3-flash[1m]",
		"ANTHROPIC_CUSTOM_MODEL_OPTION":      "glm-5.3-flash[1m]",
		"ANTHROPIC_CUSTOM_MODEL_OPTION_NAME": "GLM 5.3 Flash 1M (z.ai)",
	}
	env := readEnv(t, path)
	for k, v := range want {
		if env[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, env[k], v)
		}
	}
}

// custom has no base URL or model: the keys the preset cannot know stay out,
// and the base URL key is present but empty for the user to fill in.
func TestSkeletonCustomOmitsEmptyFields(t *testing.T) {
	dir := t.TempDir()
	custom, err := ResolveEndpoint("claude", "custom")
	if err != nil {
		t.Fatalf("ResolveEndpoint(claude, custom): %v", err)
	}
	if err := WriteSettingsSkeleton(custom, dir); err != nil {
		t.Fatalf("WriteSettingsSkeleton: %v", err)
	}
	env := readEnv(t, filepath.Join(dir, custom.Filename))
	if v, ok := env["ANTHROPIC_BASE_URL"]; !ok || v != "" {
		t.Errorf("env[ANTHROPIC_BASE_URL] = %q, %v; want present and empty", v, ok)
	}
	for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_CUSTOM_MODEL_OPTION", "ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"} {
		if _, ok := env[k]; ok {
			t.Errorf("env[%s] present, want omitted (the custom preset has none)", k)
		}
	}
}

// An empty dir would silently write into the caller's working directory —
// refused instead, so a caller bug can never land a skeleton there.
func TestSkeletonEmptyDirRefused(t *testing.T) {
	err := WriteSettingsSkeleton(Endpoint{Filename: "glm.json"}, "")
	if err == nil || !strings.Contains(err.Error(), "dir is required") {
		t.Fatalf("err = %v, want it to contain %q", err, "dir is required")
	}
	if _, statErr := os.Stat("glm.json"); statErr == nil {
		t.Fatal("skeleton was written to the working directory")
	}
}

// A file already there is the user's (or a previous run's); generation writes
// once and never overwrites, and the refusal names the path it did not touch.
func TestSkeletonExistingRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "glm.json")
	if err := os.WriteFile(path, []byte("{\"env\":{}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = WriteSettingsSkeleton(Endpoint{Filename: "glm.json"}, dir)
	if !errors.Is(err, ErrSettingsExists) {
		t.Fatalf("err = %v, want ErrSettingsExists", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want it to name the existing path %q", err, path)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("existing file changed:\nbefore %q\nafter  %q", before, after)
	}
}

// RLIMIT_FSIZE forces a real write failure — Go turns SIGXFSZ into EFBIG — so
// the generator's single error path is reachable without fakery: a failed
// stub must not survive for the next attempt to refuse.
func TestSkeletonUnwritableRemoved(t *testing.T) {
	dir := t.TempDir()
	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		t.Skipf("getrlimit: %v", err)
	}
	t.Cleanup(func() { syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old) })
	lim := old
	lim.Cur = 16 // the skeleton is a few hundred bytes; 16 forces EFBIG
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &lim); err != nil {
		t.Skipf("setrlimit: %v", err)
	}
	if err := WriteSettingsSkeleton(Endpoint{Filename: "glm.json"}, dir); err == nil {
		t.Fatal("write under a 16-byte file-size limit succeeded")
	} else if !errors.Is(err, syscall.EFBIG) {
		// Pinning the mechanism keeps this test honest: a regression that
		// fails anywhere before the write must not pass vacuously.
		t.Fatalf("err = %v, want EFBIG from the write itself", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "glm.json")); !os.IsNotExist(err) {
		t.Errorf("failed skeleton left behind (stat err %v); the retry would hit ErrSettingsExists against it", err)
	}
}

// EnsureSkeleton is the one sequence both add surfaces register through: the
// skeleton lands in the real location behind the as-typed dir, and the
// returned path keeps that as-typed form for launch to re-expand.
func TestEnsureSkeletonRegistersAsTyped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	registered, err := EnsureSkeleton("~/.claude-foo", Endpoint{BaseURL: "https://example.com", Filename: "glm.json"})
	if err != nil {
		t.Fatalf("EnsureSkeleton: %v", err)
	}
	if registered != filepath.Join("~/.claude-foo", "glm.json") {
		t.Fatalf("registered = %q, want the as-typed join", registered)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude-foo", "glm.json")); err != nil {
		t.Fatalf("skeleton not at the expanded location: %v", err)
	}
}

func TestEndpointUnknown(t *testing.T) {
	_, err := ResolveEndpoint("claude", "kimi")
	if !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("err = %v, want ErrUnknownEndpoint", err)
	}
	for _, name := range []string{"glm", "custom"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not list valid endpoint %q", err, name)
		}
	}
	// Endpoints are claude-only: a provider without a settings flag is refused
	// the same way a --settings path is, before any endpoint is looked up.
	want := "no settings flag"
	if _, err := ResolveEndpoint("codex", "glm"); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("ResolveEndpoint(codex) err = %v, want it to contain %q", err, want)
	}
}

// TestClassifySettings pins the one path-vs-keyword rule both add surfaces
// share: a keyword resolves to its preset, a path-like value — or a provider
// problem that is not about the name — defers to verbatim path behavior, and
// a bare-word typo is refused with the valid keywords listed.
func TestClassifySettings(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		value       string
		wantKeyword bool
		wantErr     []string // substrings the refusal must carry; nil = no error
	}{
		{name: "keyword resolves to its preset", provider: "claude", value: "glm", wantKeyword: true},
		{name: "custom is a keyword too", provider: "claude", value: "custom", wantKeyword: true},
		{name: "slash reads as a path", provider: "claude", value: "a/b.json"},
		{name: "dot reads as a path", provider: "claude", value: "settings.json"},
		{name: "tilde reads as a path", provider: "claude", value: "~/.claude-glm/glm.json"},
		{name: "empty is no settings", provider: "claude", value: ""},
		{name: "unknown provider defers to registration's refusal", provider: "nosuch", value: "kimi"},
		{name: "bare word typo is refused naming the valid keywords", provider: "claude", value: "kimi",
			wantErr: []string{"kimi", "glm", "custom"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ep, keyword, err := ClassifySettings(tc.provider, tc.value)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				if keyword != tc.wantKeyword {
					t.Fatalf("keyword = %t, want %t", keyword, tc.wantKeyword)
				}
				if keyword {
					want, err := ResolveEndpoint(tc.provider, tc.value)
					if err != nil || ep != want {
						t.Fatalf("ep = %+v (%v), want the resolved preset %+v", ep, err, want)
					}
				}
				return
			}
			if keyword {
				t.Fatalf("keyword = true, want false for a refusal")
			}
			if err == nil {
				t.Fatalf("err = nil, want the typo refused")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q missing %q", err, want)
				}
			}
		})
	}
}
