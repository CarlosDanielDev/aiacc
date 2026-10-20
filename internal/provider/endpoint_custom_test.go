package provider

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckBaseURL pins the poka-yoke rule both add surfaces apply to a custom
// endpoint's base URL before anything is written.
func TestCheckBaseURL(t *testing.T) {
	refused := []string{
		"http://example.com",     // plain http off the loopback
		"https://u:p@h",          // userinfo would put a secret in the file
		"https://h/?k=v",         // query
		"https://h/#frag",        // fragment
		"https://h/?",            // empty query is still a query
		"ftp://h",                // not http(s)
		"",                       // empty
		"https://",               // no host
		"api.example.com/anthro", // no scheme
		"https://h x",            // does not parse as a host
	}
	for _, raw := range refused {
		if got, err := CheckBaseURL(raw); err == nil {
			t.Errorf("CheckBaseURL(%q) = %q, want a refusal", raw, got)
		}
	}
	accepted := map[string]string{
		"http://localhost:8080":           "http://localhost:8080",
		"http://127.0.0.1:4000/v1":        "http://127.0.0.1:4000/v1",
		"http://[::1]:4000":               "http://[::1]:4000",
		"http://LocalHost:8080":           "http://LocalHost:8080",
		"https://h/x/":                    "https://h/x",
		"  https://api.example.com/anth ": "https://api.example.com/anth",
	}
	for raw, want := range accepted {
		got, err := CheckBaseURL(raw)
		if err != nil {
			t.Errorf("CheckBaseURL(%q) refused: %v", raw, err)
		} else if got != want {
			t.Errorf("CheckBaseURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A refused URL carrying user:pass@ must not echo the secret back.
func TestCheckBaseURLDoesNotEchoUserinfo(t *testing.T) {
	_, err := CheckBaseURL("https://user:s3cret@h")
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("userinfo refusal echoes the secret or is missing: %v", err)
	}
}

func TestCheckModel(t *testing.T) {
	for _, m := range []string{"", "   ", "my model", "m\t1", PlaceholderToken} {
		if err := CheckModel(m); err == nil {
			t.Errorf("CheckModel(%q) accepted", m)
		}
	}
	for _, m := range []string{"m1", "deepseek-chat", "glm-5.3-flash[1m]"} {
		if err := CheckModel(m); err != nil {
			t.Errorf("CheckModel(%q) refused: %v", m, err)
		}
	}
}

// FillEndpoint fills the custom preset from the user's answers: the model goes
// to both model fields, the display name defaults to the model, and the file
// written holds those values with the placeholder token.
func TestFillEndpointWritesSkeleton(t *testing.T) {
	custom, err := ResolveEndpoint("claude", "custom")
	if err != nil {
		t.Fatal(err)
	}
	ep, err := FillEndpoint(custom, "https://api.example.com/anthropic/", "m1", "")
	if err != nil {
		t.Fatalf("FillEndpoint: %v", err)
	}
	dir := t.TempDir()
	if err := WriteSettingsSkeleton(ep, dir); err != nil {
		t.Fatal(err)
	}
	env := readEnv(t, filepath.Join(dir, "custom.json"))
	want := map[string]string{
		"ANTHROPIC_BASE_URL":                 "https://api.example.com/anthropic",
		"ANTHROPIC_AUTH_TOKEN":               PlaceholderToken,
		"ANTHROPIC_MODEL":                    "m1",
		"ANTHROPIC_CUSTOM_MODEL_OPTION":      "m1",
		"ANTHROPIC_CUSTOM_MODEL_OPTION_NAME": "m1",
	}
	if len(env) != len(want) {
		t.Fatalf("env = %v, want %v", env, want)
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}

	ep, err = FillEndpoint(custom, "https://h", "m1", "  My Model ")
	if err != nil || ep.CustomModelOptionName != "My Model" {
		t.Fatalf("display name = %q (err %v), want the trimmed answer", ep.CustomModelOptionName, err)
	}
	if _, err := FillEndpoint(custom, "http://example.com", "m1", ""); err == nil {
		t.Fatal("FillEndpoint accepted a refused base URL")
	}
	if _, err := FillEndpoint(custom, "https://h", "my model", ""); err == nil {
		t.Fatal("FillEndpoint accepted a refused model")
	}
}
