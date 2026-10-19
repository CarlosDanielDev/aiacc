package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/session"
)

// aiMemoryFixture is a config with a claude account named for a GLM endpoint, a
// plain claude account, and a codex account, each in its own directory.
func aiMemoryFixture(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{Providers: map[string]config.Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]config.Account{
			"claude-glm": {Dir: t.TempDir()},
			"work":       {Dir: t.TempDir()},
		}},
		"codex": {EnvVar: "CODEX_HOME", Accounts: map[string]config.Account{
			"dev": {Dir: t.TempDir()},
		}},
	}}
}

// writeExecutable writes an executable file at path.
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeAIMemoryPath points PATH at a fresh directory holding a fake ai-memory
// that records its argv (NUL-separated) in argsFile and exits with status, plus
// an empty executable for each launcher name. Tests never run a real ai-memory.
func fakeAIMemoryPath(t *testing.T, status int, launchers ...string) (argsFile string) {
	t.Helper()
	bin := t.TempDir()
	argsFile = filepath.Join(t.TempDir(), "argv")
	writeExecutable(t, filepath.Join(bin, "ai-memory"),
		fmt.Sprintf("#!/bin/sh\nprintf '%%s\\0' \"$@\" > %q\nexit %d\n", argsFile, status))
	for _, l := range launchers {
		writeExecutable(t, filepath.Join(bin, l), "#!/bin/sh\n")
	}
	t.Setenv("PATH", bin)
	return argsFile
}

// recordedArgv reads the argv the fake ai-memory wrote, without the binary name.
func recordedArgv(t *testing.T, argsFile string) []string {
	t.Helper()
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("fake ai-memory did not run: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
}

// TestAIMemoryArgvPerPreset pins the argv for each preset: the harness is the
// account name for a claude* account and plain claude otherwise, the env var
// and launcher name come from the account, and the native args go last,
// unchanged, after the wrapper flags.
func TestAIMemoryArgvPerPreset(t *testing.T) {
	fakeAIMemoryPath(t, 0, "claude-glm", "work", "dev")
	c := aiMemoryFixture(t)
	glmDir := mustAccountDir(t, c, "claude", "claude-glm")
	workDir := mustAccountDir(t, c, "claude", "work")
	devDir := mustAccountDir(t, c, "codex", "dev")
	native := []string{"--dangerously-skip-permissions", "--resume", "abc123"}

	cases := []struct {
		account string
		want    []string
	}{
		{"claude-glm", []string{"run", "--env", "CLAUDE_CONFIG_DIR=" + glmDir, "--executable", "claude-glm", "claude-glm"}},
		{"work", []string{"run", "--env", "CLAUDE_CONFIG_DIR=" + workDir, "--executable", "work", "claude"}},
		{"dev", []string{"run", "--env", "CODEX_HOME=" + devDir, "--executable", "dev", "codex"}},
	}
	for _, tc := range cases {
		t.Run(tc.account, func(t *testing.T) {
			argv, err := aiMemoryInvocation(c, tc.account, native)
			if err != nil {
				t.Fatalf("invocation: %v", err)
			}
			if !strings.HasSuffix(argv[0], "ai-memory") {
				t.Errorf("argv[0] = %q, want the ai-memory binary", argv[0])
			}
			want := append(slices.Clone(tc.want), native...)
			if !slices.Equal(argv[1:], want) {
				t.Errorf("argv = %q\nwant   %q", argv[1:], want)
			}
		})
	}
}

// mustAccountDir is the account's expanded dir, for building expected argv.
func mustAccountDir(t *testing.T, c *config.Config, pn, account string) string {
	t.Helper()
	dir, err := provider.AccountDir(c, pn, account)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestAIMemoryRefusals: each way the command cannot run says why, and the
// message names the fix.
func TestAIMemoryRefusals(t *testing.T) {
	c := aiMemoryFixture(t)
	c.Providers["glab"] = config.Provider{EnvVar: "GLAB_CONFIG_DIR", Command: "glab",
		Accounts: map[string]config.Account{"me": {Dir: t.TempDir()}}}
	c.Providers["codex"] = config.Provider{EnvVar: "CODEX_HOME", Command: "codex-wrapper",
		Accounts: c.Providers["codex"].Accounts}

	t.Run("unknown account names the known ones", func(t *testing.T) {
		fakeAIMemoryPath(t, 0, "nope")
		_, err := aiMemoryInvocation(c, "nope", nil)
		if err == nil || !strings.Contains(err.Error(), `unknown account "nope"`) ||
			!strings.Contains(err.Error(), "claude-glm") {
			t.Fatalf("unknown account error = %v", err)
		}
	})
	t.Run("custom provider has no harness", func(t *testing.T) {
		fakeAIMemoryPath(t, 0, "me")
		_, err := aiMemoryInvocation(c, "me", nil)
		if err == nil || !strings.Contains(err.Error(), "glab accounts cannot run under ai-memory") {
			t.Fatalf("custom provider error = %v", err)
		}
	})
	t.Run("preset with a changed command is refused", func(t *testing.T) {
		fakeAIMemoryPath(t, 0, "dev")
		_, err := aiMemoryInvocation(c, "dev", nil)
		if err == nil || !strings.Contains(err.Error(), "codex accounts cannot run under ai-memory") {
			t.Fatalf("overridden codex error = %v", err)
		}
	})
	t.Run("preset with a changed env var is refused", func(t *testing.T) {
		fakeAIMemoryPath(t, 0, "dev")
		c := aiMemoryFixture(t)
		c.Providers["codex"] = config.Provider{EnvVar: "OTHER_HOME", Accounts: c.Providers["codex"].Accounts}
		_, err := aiMemoryInvocation(c, "dev", nil)
		if err == nil || !strings.Contains(err.Error(), "codex accounts cannot run under ai-memory") {
			t.Fatalf("overridden env error = %v", err)
		}
	})
	t.Run("missing ai-memory names the project", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := aiMemoryInvocation(c, "work", nil)
		if err == nil || !strings.Contains(err.Error(), aiMemoryURL) {
			t.Fatalf("missing ai-memory error = %v", err)
		}
	})
	t.Run("missing launcher points at setup", func(t *testing.T) {
		fakeAIMemoryPath(t, 0)
		_, err := aiMemoryInvocation(c, "work", nil)
		if err == nil || !strings.Contains(err.Error(), "aiacc setup") || !strings.Contains(err.Error(), `"work"`) {
			t.Fatalf("missing launcher error = %v", err)
		}
	})
}

// TestAIMemoryForwardsArgvAndExitStatus: the command runs the child with the
// computed argv, and a child's non-zero status comes back as an ExitStatus so
// main exits with it, while a clean run returns nil.
func TestAIMemoryForwardsArgvAndExitStatus(t *testing.T) {
	path := withTempConfig(t)
	c := aiMemoryFixture(t)
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	native := []string{"--dangerously-skip-permissions", "--resume", "abc123"}

	t.Run("clean exit", func(t *testing.T) {
		argsFile := fakeAIMemoryPath(t, 0, "claude-glm")
		cmd := newAIMemoryCmd()
		cmd.SetArgs(append([]string{"claude-glm"}, native...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("clean run: %v", err)
		}
		want := []string{"run", "--env", "CLAUDE_CONFIG_DIR=" + mustAccountDir(t, c, "claude", "claude-glm"),
			"--executable", "claude-glm", "claude-glm"}
		want = append(want, native...)
		if got := recordedArgv(t, argsFile); !slices.Equal(got, want) {
			t.Fatalf("recorded argv = %q\nwant          %q", got, want)
		}
	})
	t.Run("child status is propagated", func(t *testing.T) {
		fakeAIMemoryPath(t, 7, "claude-glm")
		cmd := newAIMemoryCmd()
		cmd.SetArgs(append([]string{"claude-glm"}, native...))
		err := cmd.Execute()
		if code, ok := ExitStatus(err); !ok || code != 7 {
			t.Fatalf("ExitStatus = %d, %v; err = %v; want 7", code, ok, err)
		}
	})
	t.Run("start failure is an ordinary error", func(t *testing.T) {
		fakeAIMemoryPath(t, 0)
		cmd := newAIMemoryCmd()
		cmd.SetArgs([]string{"work"})
		err := cmd.Execute()
		if _, ok := ExitStatus(err); ok || err == nil {
			t.Fatalf("missing launcher should be a plain error, got %v", err)
		}
	})
}

// TestExitStatusIsFoundThroughWrapping: ExitStatus sees a status however deeply
// it is wrapped, and says nothing for other errors.
func TestExitStatusIsFoundThroughWrapping(t *testing.T) {
	if code, ok := ExitStatus(fmt.Errorf("wrapped: %w", exitStatus(3))); !ok || code != 3 {
		t.Fatalf("wrapped status = %d, %v", code, ok)
	}
	if _, ok := ExitStatus(errors.New("plain")); ok {
		t.Fatal("plain error reported as an exit status")
	}
}

// TestHandoffDoneAIMemoryLine: the done screen gains the ai-memory resume line
// only while ai-memory is on PATH, and the bypass flag follows the checkbox.
func TestHandoffDoneAIMemoryLine(t *testing.T) {
	const flag = "--dangerously-skip-permissions"
	meta := session.Meta{Cwd: "/Users/x/proj"}
	const want = "or with ai-memory: aiacc ai-memory personal --resume abc123"
	const wantOn = "or with ai-memory: aiacc ai-memory personal " + flag + " --resume abc123"

	t.Run("present with ai-memory and a bypass checkbox", func(t *testing.T) {
		fakeAIMemoryPath(t, 0)
		_, body := captureHandoffDone(t)
		if err := showDone(flag, "personal", meta, "abc123"); err != nil {
			t.Fatalf("showDone: %v", err)
		}
		if got := resumeLines((*body)(false)); got[len(got)-1] != want {
			t.Errorf("unchecked ai-memory line = %q", got[len(got)-1])
		}
		if got := resumeLines((*body)(true)); got[len(got)-1] != wantOn {
			t.Errorf("checked ai-memory line = %q", got[len(got)-1])
		}
	})
	t.Run("present on the plain screen", func(t *testing.T) {
		fakeAIMemoryPath(t, 0)
		lines := captureHandoffMessage(t)
		if err := showDone("", "personal", meta, "abc123"); err != nil {
			t.Fatalf("showDone: %v", err)
		}
		if got := resumeLines(*lines); got[len(got)-1] != want {
			t.Errorf("plain ai-memory line = %q", got[len(got)-1])
		}
	})
	t.Run("absent without ai-memory on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		lines := captureHandoffMessage(t)
		if err := showDone("", "personal", meta, "abc123"); err != nil {
			t.Fatalf("showDone: %v", err)
		}
		if !slices.Equal(*lines, handoffDoneLines("personal", meta, "abc123")) {
			t.Fatalf("screen changed without ai-memory:\n%v", *lines)
		}
	})
}

// fakeAIMemoryScript writes a fake ai-memory with body into a fresh directory
// and returns its path, for tests that drive runAIMemory directly.
func fakeAIMemoryScript(t *testing.T, body string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ai-memory")
	writeExecutable(t, bin, "#!/bin/sh\n"+body)
	return bin
}

// TestAIMemorySignalDelivery sends real signals to aiacc's own process while the
// child runs, and records what the child receives. The terminal delivers Ctrl-C
// (SIGINT) and Ctrl-backslash (SIGQUIT) to the child through the process group, so aiacc
// must absorb them and never relay them: the child gets each of those zero times
// from aiacc. SIGTERM and SIGHUP are not delivered by the terminal, so aiacc relays
// each one exactly once. The child stops on a file rather than a signal, so every
// delivery is recorded before it exits.
func TestAIMemorySignalDelivery(t *testing.T) {
	dir := t.TempDir()
	ready, stop, log := filepath.Join(dir, "ready"), filepath.Join(dir, "stop"), filepath.Join(dir, "log")
	// Bounded loop so a missing stop file ends the child, not the run.
	bin := fakeAIMemoryScript(t, fmt.Sprintf(
		"for s in INT QUIT TERM HUP; do trap \"echo $s >> %[1]q\" $s; done\n"+
			"echo ready > %[2]q\n"+
			"i=0; while [ ! -f %[3]q ] && [ $i -lt 400 ]; do sleep 0.05; i=$((i+1)); done\n"+
			"exit 0\n", log, ready, stop))

	errc := make(chan error, 1)
	go func() { errc <- runAIMemory(bin, []string{"run"}) }()
	waitForFile(t, ready)
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP} {
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Wait for the two relayed signals, then give any stray INT or QUIT time to show.
	waitFor(t, 5*time.Second, func() bool { return countLines(t, log, "TERM") >= 1 && countLines(t, log, "HUP") >= 1 }, "TERM and HUP to reach the child")
	time.Sleep(200 * time.Millisecond)
	writeExecutable(t, stop, "")

	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("child run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("aiacc did not finish after the child was stopped")
	}
	want := map[string]int{"INT": 0, "QUIT": 0, "TERM": 1, "HUP": 1}
	for name, n := range want {
		if got := countLines(t, log, name); got != n {
			t.Errorf("child received %s %d times from aiacc, want %d", name, got, n)
		}
	}
}

// countLines counts the lines of the child's signal log equal to name.
func countLines(t *testing.T, log, name string) int {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(string(raw), "\n") {
		if l == name {
			n++
		}
	}
	return n
}

// waitFor polls cond until it holds, failing the test after d.
func waitFor(t *testing.T, d time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitForFile polls until path exists, failing the test after a few seconds.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAIMemorySignalKilledChildMapsToShellStatus: a child that dies to a signal
// makes aiacc exit 128+signo, the shell convention, not a flat 1.
func TestAIMemorySignalKilledChildMapsToShellStatus(t *testing.T) {
	c := aiMemoryFixture(t)
	path := withTempConfig(t)
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	fakeAIMemoryPath(t, 0, "claude-glm")
	old := runAIMemory
	runAIMemory = func(string, []string) error {
		return exec.Command("/bin/sh", "-c", "kill -KILL $$").Run()
	}
	t.Cleanup(func() { runAIMemory = old })

	cmd := newAIMemoryCmd()
	cmd.SetArgs([]string{"claude-glm"})
	if code, ok := ExitStatus(cmd.Execute()); !ok || code != 128+int(syscall.SIGKILL) {
		t.Fatalf("ExitStatus = %d, %v; want %d", code, ok, 128+int(syscall.SIGKILL))
	}
}

// TestAIMemoryAccountDirIsAbsolute: a relative account dir is handed to
// ai-memory as an absolute path, so ai-memory does not resolve it against its
// own working directory.
func TestAIMemoryAccountDirIsAbsolute(t *testing.T) {
	fakeAIMemoryPath(t, 0, "claude-glm")
	c := aiMemoryFixture(t)
	c.Providers["claude"].Accounts["claude-glm"] = config.Account{Dir: "rel/glm"}
	argv, err := aiMemoryInvocation(c, "claude-glm", nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs("rel/glm")
	if err != nil {
		t.Fatal(err)
	}
	if got := "CLAUDE_CONFIG_DIR=" + want; !slices.Contains(argv, got) {
		t.Fatalf("argv %q does not carry %s", argv, got)
	}
}
