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
	path, err := WriteSettingsSkeleton("claude", "glm", dir)
	if err != nil {
		t.Fatalf("WriteSettingsSkeleton: %v", err)
	}
	if path != filepath.Join(dir, "glm.json") {
		t.Fatalf("path = %q, want %q", path, filepath.Join(dir, "glm.json"))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600 (the file is about to hold a key)", info.Mode().Perm())
	}
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
	want := map[string]string{
		"ANTHROPIC_BASE_URL":                 "https://api.z.ai/api/anthropic",
		"ANTHROPIC_AUTH_TOKEN":               PlaceholderToken,
		"ANTHROPIC_MODEL":                    "glm-5.3-flash[1m]",
		"ANTHROPIC_CUSTOM_MODEL_OPTION":      "glm-5.3-flash[1m]",
		"ANTHROPIC_CUSTOM_MODEL_OPTION_NAME": "GLM 5.3 Flash 1M (z.ai)",
	}
	for k, v := range want {
		if doc.Env[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, doc.Env[k], v)
		}
	}
}

// custom has no base URL or model: the keys the preset cannot know stay out,
// and the base URL key is present but empty for the user to fill in.
func TestSkeletonCustomOmitsEmptyFields(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteSettingsSkeleton("claude", "custom", dir)
	if err != nil {
		t.Fatalf("WriteSettingsSkeleton: %v", err)
	}
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
	if v, ok := doc.Env["ANTHROPIC_BASE_URL"]; !ok || v != "" {
		t.Errorf("env[ANTHROPIC_BASE_URL] = %q, %v; want present and empty", v, ok)
	}
	for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_CUSTOM_MODEL_OPTION", "ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"} {
		if _, ok := doc.Env[k]; ok {
			t.Errorf("env[%s] present, want omitted (the custom preset has none)", k)
		}
	}
}

// An empty dir would silently write into the caller's working directory —
// refused instead, so a caller bug can never land a skeleton there.
func TestSkeletonEmptyDirRefused(t *testing.T) {
	_, err := WriteSettingsSkeleton("claude", "glm", "")
	if err == nil || !strings.Contains(err.Error(), "dir is required") {
		t.Fatalf("err = %v, want it to contain %q", err, "dir is required")
	}
	if _, statErr := os.Stat("glm.json"); statErr == nil {
		t.Fatal("skeleton was written to the working directory")
	}
}

// A file already there is the user's (or a previous run's); generation writes
// once and never overwrites, and hands back the path it refused to touch.
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
	got, err := WriteSettingsSkeleton("claude", "glm", dir)
	if !errors.Is(err, ErrSettingsExists) {
		t.Fatalf("err = %v, want ErrSettingsExists", err)
	}
	if got != path {
		t.Fatalf("returned path = %q, want the existing %q", got, path)
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
	if _, err := WriteSettingsSkeleton("claude", "glm", dir); err == nil {
		t.Fatal("write under a 16-byte file-size limit succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "glm.json")); !os.IsNotExist(err) {
		t.Errorf("failed skeleton left behind (stat err %v); the retry would hit ErrSettingsExists against it", err)
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
	if _, err := WriteSettingsSkeleton("codex", "glm", t.TempDir()); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("WriteSettingsSkeleton(codex) err = %v, want it to contain %q", err, want)
	}
}
