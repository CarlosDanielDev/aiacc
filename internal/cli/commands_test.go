package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
)

// seedConfig writes a single-provider config with one account at dir and
// returns nothing; callers pass the path from withTempConfig.
func seedConfig(t *testing.T, path, dir string, quota int) {
	t.Helper()
	cfg := &config.Config{Providers: map[string]config.Provider{
		"claude": {
			EnvVar: "CLAUDE_CONFIG_DIR",
			Accounts: map[string]config.Account{
				"work": {Dir: dir, Quota: quota},
			},
		},
	}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

func TestUsePrintsExportLine(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	seedConfig(t, path, dir, 0)

	cmd := newUseCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"claude", "work", "--shell", "bash"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("use: %v", err)
	}

	want, err := shell.ExportLine("bash", "CLAUDE_CONFIG_DIR", dir)
	if err != nil {
		t.Fatalf("export line: %v", err)
	}
	if strings.TrimSpace(out.String()) != want {
		t.Fatalf("use output = %q, want %q", out.String(), want)
	}
}

func TestUseMissingDirErrors(t *testing.T) {
	path := withTempConfig(t)
	missing := filepath.Join(t.TempDir(), "gone")
	seedConfig(t, path, missing, 0)

	cmd := newUseCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"claude", "work", "--shell", "bash"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing account dir")
	}
}

func TestShellInitPrintsHook(t *testing.T) {
	cmd := newShellInitCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"bash"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("shell-init: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "aiacc()") || !strings.Contains(s, "eval") {
		t.Fatalf("hook output missing markers: %q", s)
	}
}

func TestStatusMarksActiveAccount(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()
	seedConfig(t, path, dir, 0)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "*") {
		t.Fatalf("status did not mark active account: %q", out.String())
	}
}

func TestUsageSumsTotals(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	logDir := filepath.Join(dir, "projects", "x")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lines := `{"message":{"usage":{"input_tokens":10,"output_tokens":5}}}
{"message":{"usage":{"input_tokens":20,"output_tokens":7}}}
`
	if err := os.WriteFile(filepath.Join(logDir, "s.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	seedConfig(t, path, dir, 0)

	cmd := newUsageCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("usage: %v", err)
	}
	// input 30, output 12, total 42.
	if !strings.Contains(out.String(), "42") {
		t.Fatalf("usage total not shown: %q", out.String())
	}
}
