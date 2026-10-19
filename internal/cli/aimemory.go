package cli

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/spf13/cobra"
)

// aiMemoryURL is the ai-memory project, named when its binary is missing.
const aiMemoryURL = "https://github.com/akitaonrails/ai-memory"

// relayedSignals are the signals aiacc forwards to the child: the ones the
// terminal does not deliver to the foreground process group, which holds the
// child. SIGHUP is also sent to that group when the terminal hangs up, so a
// hangup can reach the child twice; the session is ending then anyway.
var relayedSignals = []os.Signal{syscall.SIGTERM, syscall.SIGHUP}

// absorbedSignals are received and dropped while the child runs. The terminal
// already delivers SIGINT (Ctrl-C) and SIGQUIT (Ctrl-backslash) to the child, so relaying
// them as well would hand the child each keypress twice, and a second Ctrl-C can
// read as quit. Receiving them keeps aiacc alive to wait for the child.
var absorbedSignals = []os.Signal{syscall.SIGINT, syscall.SIGQUIT}

// runAIMemory is a variable so one test can stub the process run to get a fixed
// child status. lookPath and signalSource are plain variables that no test
// replaces: lookup tests put a fake ai-memory on PATH, and the signal test sends
// real signals to the test process, so it needs the real signal.Notify.
// signalSource returns the channel relayed and absorbed signals arrive on, and a
// stop func.
var (
	lookPath     = exec.LookPath
	runAIMemory  = execAIMemory
	signalSource = func() (<-chan os.Signal, func()) {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, slices.Concat(relayedSignals, absorbedSignals)...)
		return ch, func() { signal.Stop(ch) }
	}
)

// execAIMemory runs bin attached to this terminal and returns when the child
// exits. aiacc stays alive for the child's whole run: it absorbs the signals the
// terminal already delivers and relays the rest, so the child is never orphaned.
func execAIMemory(bin string, args []string) error {
	sigs, stop := signalSource() // before Start, so no early signal kills aiacc
	defer stop()
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	go relaySignals(cmd.Process, sigs, done)
	return cmd.Wait()
}

// relaySignals delivers each relayed signal from sigs to the child until done
// closes, and drops the absorbed ones. A signal that arrives after the child has
// been reaped fails to deliver, which is harmless.
func relaySignals(child *os.Process, sigs <-chan os.Signal, done <-chan struct{}) {
	for {
		select {
		case s := <-sigs:
			if slices.Contains(relayedSignals, s) {
				_ = child.Signal(s)
			}
		case <-done:
			return
		}
	}
}

// childStatus is the status aiacc exits with for a child that ended with err: its
// own code, or 128+signo when a signal killed it (the shell convention).
func childStatus(ee *exec.ExitError) int {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	if code := ee.ExitCode(); code >= 0 {
		return code
	}
	return 1
}

// exitStatus is a child's non-zero exit status, carried up to main so aiacc
// exits with the same status and prints nothing of its own.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// ExitStatus reports the status a child process left, when err carries one.
func ExitStatus(err error) (int, bool) {
	var e exitStatus
	if errors.As(err, &e) {
		return int(e), true
	}
	return 0, false
}

// aiMemoryOnPath reports whether ai-memory can be run from PATH.
func aiMemoryOnPath() bool {
	_, err := lookPath("ai-memory")
	return err == nil
}

func newAIMemoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ai-memory <account> [native args...]",
		Short: "Run an account under ai-memory, with its launcher and config dir",
		Long: "Run one account under ai-memory's managed workstream. ai-memory treats a " +
			"claude-glm style name as a label only, so this passes the launcher with " +
			"--executable and the account's config dir with --env, which keeps ai-memory's " +
			"session store and hooks on the same config home as the account.\n\n" +
			"Everything after the account is the harness's own: --resume, " +
			"--dangerously-skip-permissions and the rest are forwarded unchanged, not " +
			"read by aiacc. The exit status of the harness is this command's.\n\n" +
			"Needs ai-memory on PATH (" + aiMemoryURL + ") and the account's launcher, " +
			"which `aiacc setup` installs.",
		Args:               cobra.MinimumNArgs(1),
		SilenceUsage:       true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "-h" || args[0] == "--help" {
				return cmd.Help()
			}
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			argv, err := aiMemoryInvocation(c, args[0], args[1:])
			if err != nil {
				return err
			}
			if err := runAIMemory(argv[0], argv[1:]); err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					return exitStatus(childStatus(ee))
				}
				return err
			}
			return nil
		},
	}
}

// aiMemoryInvocation builds the full command line for running account under
// ai-memory: argv[0] is the ai-memory binary, the rest its arguments. The
// wrapper-owned --env and --executable flags precede the harness name, which
// ai-memory requires for --env; the native args follow it unchanged.
func aiMemoryInvocation(c *config.Config, account string, native []string) ([]string, error) {
	pn, ok := ownerOf(c, account)
	if !ok {
		return nil, unknownAccountError(c, account)
	}
	harness, err := aiMemoryHarness(c, pn, account)
	if err != nil {
		return nil, err
	}
	env, _ := provider.EnvVar(c, pn)
	dir, err := provider.AccountDir(c, pn, account)
	if err != nil {
		return nil, err
	}
	bin, err := lookPath("ai-memory")
	if err != nil {
		return nil, fmt.Errorf("ai-memory is not on PATH: install it from %s", aiMemoryURL)
	}
	if _, err := lookPath(account); err != nil {
		return nil, fmt.Errorf("no launcher named %q on PATH: run `aiacc setup` to install the account launchers", account)
	}
	// ai-memory resolves a relative dir against its own working directory, not
	// ours, so the dir it is handed is absolute.
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	argv := []string{bin, "run", "--env", env + "=" + abs, "--executable", account, harness}
	return append(argv, native...), nil
}

// aiMemoryHarness is the ai-memory harness name for an account of provider pn.
// ai-memory reads any name starting with "claude" as its Claude harness, so a
// claude account keeps its own name when it has one. Only the built-in claude
// and codex CLIs have a harness; a custom provider, or a preset whose command or
// env var was changed, is refused rather than run under the wrong CLI.
func aiMemoryHarness(c *config.Config, pn, account string) (string, error) {
	pr, builtin := provider.Presets[pn]
	cmd, _ := provider.Command(c, pn)
	env, _ := provider.EnvVar(c, pn)
	if !builtin || cmd != pr.Command || env != pr.EnvVar {
		return "", fmt.Errorf("%s accounts cannot run under ai-memory: it has a harness only for the built-in claude and codex CLIs", pn)
	}
	switch pn {
	case "claude":
		if strings.HasPrefix(account, "claude") {
			return account, nil
		}
		return "claude", nil
	default: // codex
		return "codex", nil
	}
}

// unknownAccountError names the registered accounts, the way the other commands
// do, so a typo is one glance from the right name.
func unknownAccountError(c *config.Config, account string) error {
	seen := map[string]bool{}
	for _, p := range c.Providers {
		for an := range p.Accounts {
			seen[an] = true
		}
	}
	if len(seen) == 0 {
		return fmt.Errorf("unknown account %q: no accounts are registered yet, see `aiacc add`", account)
	}
	return fmt.Errorf("unknown account %q; known accounts: %s", account, strings.Join(slices.Sorted(maps.Keys(seen)), ", "))
}
