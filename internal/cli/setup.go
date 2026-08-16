package cli

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Guided one-time shell setup for the launcher commands",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isTerminal(os.Stdin) {
				// No terminal to drive the wizard — print the manual steps.
				sh := detectShell()
				line, _ := shell.RcLine(sh)
				rc, _ := shell.RcPath(sh)
				fmt.Fprintf(cmd.OutOrStdout(),
					"Add this line to %s:\n  %s\nThen reload it:\n  %s\n",
					tildeize(rc), line, shell.ReloadCmd(sh, tildeize(rc)))
				return nil
			}
			return runSetup()
		},
	}
}

// runSetup drives the framed, step-by-step shell-setup screen: it installs the
// launcher line into the shell startup file (step 1, the one thing it can do for
// you) and shows the exact reload/use commands for steps 2–3.
func runSetup() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	sh := detectShell()
	line, err := shell.RcLine(sh)
	if err != nil {
		return err
	}
	rc, err := shell.RcPath(sh)
	if err != nil {
		return err
	}
	info := tui.SetupInfo{
		Shell:          sh,
		RcLine:         line,
		RcPath:         tildeize(rc),
		ReloadCmd:      shell.ReloadCmd(sh, tildeize(rc)),
		Example:        exampleLauncher(path),
		AlreadyPresent: fileContains(rc, line),
	}
	return tui.RunSetup(info, func() error { return appendLine(rc, line) })
}

// setupNeeded reports whether the launcher line is missing from the shell startup
// file, so the picker can nudge the user toward `s`.
func setupNeeded() bool {
	sh := detectShell()
	line, err := shell.RcLine(sh)
	if err != nil {
		return false
	}
	rc, err := shell.RcPath(sh)
	if err != nil {
		return false
	}
	return !fileContains(rc, line)
}

// detectShell is the current shell's basename if aiacc supports it, else bash.
func detectShell() string {
	s := "bash"
	if sh := os.Getenv("SHELL"); sh != "" {
		s = filepath.Base(sh)
	}
	switch s {
	case "bash", "zsh", "fish":
		return s
	default:
		return "bash"
	}
}

// exampleLauncher is the first launchable account name (a real command the user
// will have), shown in the setup screen's "use it" step, or "aiacc" as a
// fallback when nothing is registered yet.
func exampleLauncher(cfgPath string) string {
	c, err := config.Load(cfgPath)
	if err != nil {
		return "aiacc"
	}
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		if launchCommand(pn) == "" {
			continue
		}
		for _, an := range slices.Sorted(maps.Keys(c.Providers[pn].Accounts)) {
			return an
		}
	}
	return "aiacc"
}

// appendLine adds line to rc, idempotently — an existing line is left untouched
// so re-running never duplicates it. The parent directory is created for fish.
func appendLine(rc, line string) error {
	if fileContains(rc, line) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(rc), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n# aiacc launchers\n%s\n", line)
	return err
}

func fileContains(path, needle string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), needle)
}

// tildeize replaces the home-dir prefix of p with ~ for display.
func tildeize(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
