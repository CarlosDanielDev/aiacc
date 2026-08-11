package shell

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestExportLineGolden(t *testing.T) {
	tests := []struct {
		shell string
		want  string
	}{
		{"bash", `export CLAUDE_CONFIG_DIR='/Users/x/.claude-work'`},
		{"zsh", `export CLAUDE_CONFIG_DIR='/Users/x/.claude-work'`},
		{"fish", `set -gx CLAUDE_CONFIG_DIR '/Users/x/.claude-work'`},
	}
	for _, tt := range tests {
		got, err := ExportLine(tt.shell, "CLAUDE_CONFIG_DIR", "/Users/x/.claude-work")
		if err != nil {
			t.Fatalf("ExportLine(%q): unexpected error %v", tt.shell, err)
		}
		if got != tt.want {
			t.Errorf("ExportLine(%q) = %q, want %q", tt.shell, got, tt.want)
		}
	}
}

func TestExportLineSingleQuoteEscape(t *testing.T) {
	// A dir with an embedded quote must be escaped with the '\'' idiom so it
	// cannot terminate the surrounding single-quoted string.
	got, err := ExportLine("bash", "X", `a'b`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `export X='a'\''b'`
	if got != want {
		t.Fatalf("ExportLine escape = %q, want %q", got, want)
	}
}

// TestExportLineNoBreakout proves via a real shell that a hostile directory
// value cannot break out of the quotes and execute commands.
func TestExportLineNoBreakout(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	// Hostile value: a quote to try to close the string, shell metacharacters,
	// and a $()-substitution that must stay literal. If any of it were honored,
	// $CLAUDE_CONFIG_DIR would differ from dir (e.g. truncate at the quote, or
	// have echo output prepended). Byte-for-byte equality proves no breakout.
	dir := `/tmp/a'; echo INJECTED; $(echo sub) '`
	line, err := ExportLine("bash", "CLAUDE_CONFIG_DIR", dir)
	if err != nil {
		t.Fatalf("ExportLine: %v", err)
	}
	out, err := exec.Command(bash, "-c", line+`; printf '%s' "$CLAUDE_CONFIG_DIR"`).CombinedOutput()
	if err != nil {
		t.Fatalf("bash eval failed: %v (out=%q)", err, out)
	}
	if string(out) != dir {
		t.Fatalf("breakout: bash saw %q, want %q", out, dir)
	}
}

func TestExportLineFishBackslashEscaped(t *testing.T) {
	// fish treats backslash as special inside single quotes, so it must be
	// doubled; a bare '\'' idiom (correct for bash) would be an injection under
	// fish. These goldens run without a fish binary present.
	cases := []struct{ dir, want string }{
		{`a\`, `set -gx X 'a\\'`},
		{`a'b`, `set -gx X 'a\'b'`},
		{`a\'b`, `set -gx X 'a\\\'b'`},
	}
	for _, c := range cases {
		got, err := ExportLine("fish", "X", c.dir)
		if err != nil {
			t.Fatalf("ExportLine(fish, %q): %v", c.dir, err)
		}
		if got != c.want {
			t.Errorf("ExportLine(fish, %q) = %q, want %q", c.dir, got, c.want)
		}
	}
}

// TestExportLineFishNoBreakout proves via a real fish shell that a hostile
// directory value cannot break out of the quotes and execute commands.
func TestExportLineFishNoBreakout(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish not available")
	}
	// A trailing backslash before a quote is the fish-specific breakout the POSIX
	// idiom misses; the appended commands must stay literal.
	dir := `/tmp/a\'; touch INJECTED; echo '`
	line, err := ExportLine("fish", "AIACC_T", dir)
	if err != nil {
		t.Fatalf("ExportLine: %v", err)
	}
	out, err := exec.Command(fish, "-c", line+`; printf '%s' $AIACC_T`).CombinedOutput()
	if err != nil {
		t.Fatalf("fish eval failed: %v (out=%q)", err, out)
	}
	if string(out) != dir {
		t.Fatalf("breakout: fish saw %q, want %q", out, dir)
	}
}

func TestExportLineNewlineDirRejected(t *testing.T) {
	if _, err := ExportLine("bash", "X", "/tmp/a\nb"); !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("want ErrUnsafeDir for newline, got %v", err)
	}
}

func TestExportLineControlCharsRejected(t *testing.T) {
	for _, dir := range []string{"", "/tmp/a\rb", "/tmp/a\x00b", "/tmp/a\tb", "/tmp/a\x1fb"} {
		if _, err := ExportLine("bash", "X", dir); !errors.Is(err, ErrUnsafeDir) {
			t.Fatalf("want ErrUnsafeDir for %q, got %v", dir, err)
		}
	}
}

func TestExportLineInvalidEnvVarRejected(t *testing.T) {
	for _, name := range []string{"", "1ABC", "A-B", "A B", "A$", "A.B"} {
		if _, err := ExportLine("bash", name, "/tmp/x"); !errors.Is(err, ErrInvalidEnvVar) {
			t.Fatalf("want ErrInvalidEnvVar for %q, got %v", name, err)
		}
	}
}

func TestExportLineValidEnvVarAccepted(t *testing.T) {
	for _, name := range []string{"A", "_x", "CLAUDE_CONFIG_DIR", "_", "a1_2"} {
		if _, err := ExportLine("bash", name, "/tmp/x"); err != nil {
			t.Fatalf("valid env var %q rejected: %v", name, err)
		}
	}
}

func TestExportLineUnknownShell(t *testing.T) {
	if _, err := ExportLine("tcsh", "X", "/tmp/x"); !errors.Is(err, ErrUnknownShell) {
		t.Fatalf("want ErrUnknownShell, got %v", err)
	}
}

func TestHookBash(t *testing.T) {
	got, err := Hook("bash")
	if err != nil {
		t.Fatalf("Hook(bash): %v", err)
	}
	for _, want := range []string{"aiacc()", "eval", "command aiacc", "--shell bash"} {
		if !strings.Contains(got, want) {
			t.Errorf("Hook(bash) missing %q in:\n%s", want, got)
		}
	}
}

func TestHookZsh(t *testing.T) {
	got, err := Hook("zsh")
	if err != nil {
		t.Fatalf("Hook(zsh): %v", err)
	}
	if !strings.Contains(got, "--shell zsh") || !strings.Contains(got, "eval") {
		t.Errorf("Hook(zsh) wrong body:\n%s", got)
	}
}

func TestHookFish(t *testing.T) {
	got, err := Hook("fish")
	if err != nil {
		t.Fatalf("Hook(fish): %v", err)
	}
	for _, want := range []string{"function aiacc", "end", "eval", "command aiacc", "--shell fish"} {
		if !strings.Contains(got, want) {
			t.Errorf("Hook(fish) missing %q in:\n%s", want, got)
		}
	}
}

func TestHookUnknownShell(t *testing.T) {
	if _, err := Hook("tcsh"); !errors.Is(err, ErrUnknownShell) {
		t.Fatalf("want ErrUnknownShell, got %v", err)
	}
}
