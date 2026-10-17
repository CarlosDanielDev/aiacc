package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// shellBinary finds an installed shell. TestMain narrows PATH to a temp bin plus
// /usr/bin and /bin, so besides PATH it checks the usual install prefixes.
func shellBinary(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"} {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// runShellInit runs `aiacc shell-init <shell>` and returns what it printed.
func runShellInit(t *testing.T, shellName string) (string, error) {
	t.Helper()
	cmd := newShellInitCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{shellName})
	err := cmd.Execute()
	return out.String(), err
}

// The retired stub must exit 0 and print only comment lines (plus the one-time
// erase), so a startup file that still evals it stays harmless.
func TestShellInitStubIsRetired(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish"} {
		out, err := runShellInit(t, sh)
		if err != nil {
			t.Fatalf("shell-init %s: %v", sh, err)
		}
		if !strings.Contains(out, "aiacc setup") {
			t.Errorf("shell-init %s: no pointer to aiacc setup:\n%s", sh, out)
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if sh == "fish" && line == "functions -q aiacc; and functions -e aiacc" {
				continue
			}
			if sh != "fish" && line == "unset -f aiacc 2>/dev/null || true" {
				continue
			}
			t.Errorf("shell-init %s printed a non-comment line %q", sh, line)
		}
	}
}

func TestShellInitHiddenFromHelp(t *testing.T) {
	root := NewRoot()
	var help bytes.Buffer
	root.SetOut(&help)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	if strings.Contains(help.String(), "shell-init") {
		t.Fatalf("shell-init must be hidden from help:\n%s", help.String())
	}
}

// The stub's output, eval'd by the shell a user actually runs, is a no-op that
// exits 0. Each shell runs with its startup files disabled and HOME in a temp dir,
// so no rc file is read or touched. A shell that is not installed is skipped.
func TestShellInitStubEvalIsNoop(t *testing.T) {
	cases := []struct {
		shell string
		bin   string
		args  []string
	}{
		{"sh", "sh", nil},
		{"bash", "bash", []string{"--norc", "--noprofile"}},
		{"zsh", "zsh", []string{"-f"}},
		{"fish", "fish", []string{"--no-config"}},
	}
	for _, c := range cases {
		t.Run(c.shell, func(t *testing.T) {
			bin, ok := shellBinary(c.bin)
			if !ok {
				t.Skipf("%s not installed", c.bin)
			}
			stub, err := runShellInit(t, c.shell)
			if err != nil {
				t.Fatalf("shell-init %s: %v", c.shell, err)
			}
			home := t.TempDir()
			run := func(prog string) (string, string, error) {
				args := append(append([]string{}, c.args...), "-c", prog)
				cmd := exec.Command(bin, args...)
				cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "AIACC_STUB="+stub)
				var out, errb bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &errb
				err := cmd.Run()
				return out.String(), errb.String(), err
			}
			out, errs, err := run(`eval "$AIACC_STUB"; echo ok`)
			if err != nil || strings.TrimSpace(out) != "ok" || errs != "" {
				t.Fatalf("eval of stub: err=%v stdout=%q stderr=%q\nstub:\n%s", err, out, errs, stub)
			}
		})
	}
}

// The one-time erase removes a leftover pre-v0.7 `aiacc` function from a live
// shell that re-sources the stub. Only bash, zsh and fish have that erase line.
func TestShellInitStubErasesOldHook(t *testing.T) {
	cases := []struct {
		shell string
		bin   string
		args  []string
		prog  string
	}{
		{"bash", "bash", []string{"--norc", "--noprofile"}, `aiacc() { echo old; }; eval "$AIACC_STUB"; if type aiacc >/dev/null 2>&1; then echo still; else echo gone; fi`},
		{"zsh", "zsh", []string{"-f"}, `aiacc() { echo old; }; eval "$AIACC_STUB"; if (( ${+functions[aiacc]} )); then echo still; else echo gone; fi`},
		{"fish", "fish", []string{"--no-config"}, `function aiacc; echo old; end; eval "$AIACC_STUB"; if functions -q aiacc; echo still; else; echo gone; end`},
	}
	for _, c := range cases {
		t.Run(c.shell, func(t *testing.T) {
			bin, ok := shellBinary(c.bin)
			if !ok {
				t.Skipf("%s not installed", c.bin)
			}
			stub, err := runShellInit(t, c.shell)
			if err != nil {
				t.Fatalf("shell-init %s: %v", c.shell, err)
			}
			home := t.TempDir()
			args := append(append([]string{}, c.args...), "-c", c.prog)
			cmd := exec.Command(bin, args...)
			cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "AIACC_STUB="+stub)
			out, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != "gone" {
				t.Fatalf("want old hook erased (gone), got err=%v out=%q", err, out)
			}
		})
	}
}
