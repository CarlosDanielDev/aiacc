package shell

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLauncherGolden(t *testing.T) {
	tests := []struct {
		shell string
		want  string
	}{
		{"bash", "claude-work() { CLAUDE_CONFIG_DIR='/home/x/.claude-work' command claude \"$@\"; }\n"},
		{"zsh", "claude-work() { CLAUDE_CONFIG_DIR='/home/x/.claude-work' command claude \"$@\"; }\n"},
		{"fish", "function claude-work; env CLAUDE_CONFIG_DIR='/home/x/.claude-work' claude $argv; end\n"},
	}
	for _, tt := range tests {
		got, err := Launcher(tt.shell, "claude-work", "claude", "CLAUDE_CONFIG_DIR", "/home/x/.claude-work")
		if err != nil {
			t.Fatalf("Launcher(%q): %v", tt.shell, err)
		}
		if got != tt.want {
			t.Errorf("Launcher(%q) =\n%q\nwant\n%q", tt.shell, got, tt.want)
		}
	}
}

// TestLauncherSkipsUnsafeTokens: a name, command, or env var that isn't a safe
// token yields "" (no error), so the caller skips it — never written into a
// sourced script.
func TestLauncherSkipsUnsafeTokens(t *testing.T) {
	cases := []struct{ name, cmd, env string }{
		{"claude work", "claude", "CLAUDE_CONFIG_DIR"},  // space in name
		{"a;rm", "claude", "CLAUDE_CONFIG_DIR"},         // metachar in name
		{"claude-work", "cla ude", "CLAUDE_CONFIG_DIR"}, // space in command
		{"claude-work", "claude", "BAD VAR"},            // space in env var
		{"claude-work", "claude", ""},                   // empty env var
		{"claude-work", "", "CLAUDE_CONFIG_DIR"},        // empty command
	}
	for _, c := range cases {
		got, err := Launcher("bash", c.name, c.cmd, c.env, "/x")
		if err != nil {
			t.Fatalf("Launcher(%+v): unexpected error %v", c, err)
		}
		if got != "" {
			t.Errorf("Launcher(%+v) = %q, want skipped", c, got)
		}
	}
}

func TestLauncherUnsafeDirRejected(t *testing.T) {
	for _, dir := range []string{"", "/tmp/a\nb", "/tmp/a\x00b", "/tmp/a\tb"} {
		if _, err := Launcher("bash", "claude-work", "claude", "CLAUDE_CONFIG_DIR", dir); !errors.Is(err, ErrUnsafeDir) {
			t.Fatalf("want ErrUnsafeDir for %q, got %v", dir, err)
		}
	}
}

func TestLauncherUnknownShell(t *testing.T) {
	if _, err := Launcher("tcsh", "claude-work", "claude", "CLAUDE_CONFIG_DIR", "/x"); !errors.Is(err, ErrUnknownShell) {
		t.Fatalf("want ErrUnknownShell, got %v", err)
	}
}

// TestLauncherNoBreakoutBash proves via a real bash that a hostile directory
// value cannot break out of the quotes when the function definition is sourced.
// A bad quote would let the injected `touch INJECTED` run at definition time.
func TestLauncherNoBreakoutBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "INJECTED")
	dir := `/tmp/a'; touch ` + marker + `; echo '`
	fn, err := Launcher("bash", "prof", "true", "CLAUDE_CONFIG_DIR", dir)
	if err != nil {
		t.Fatalf("Launcher: %v", err)
	}
	// Source the function definition; it must define, not execute the injection.
	if out, err := exec.Command(bash, "-c", fn).CombinedOutput(); err != nil {
		t.Fatalf("sourcing function failed: %v (%s)", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("breakout: injected command ran while sourcing the launcher")
	}
}

// TestLauncherNoBreakoutFish is the fish-specific counterpart: a trailing
// backslash before a quote is the breakout the POSIX idiom misses.
func TestLauncherNoBreakoutFish(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish not available")
	}
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "INJECTED")
	dir := `/tmp/a\'; touch ` + marker + `; echo '`
	fn, err := Launcher("fish", "prof", "true", "CLAUDE_CONFIG_DIR", dir)
	if err != nil {
		t.Fatalf("Launcher: %v", err)
	}
	if out, err := exec.Command(fish, "-c", fn).CombinedOutput(); err != nil {
		t.Fatalf("sourcing function failed: %v (%s)", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("breakout: injected command ran while sourcing the fish launcher")
	}
}
