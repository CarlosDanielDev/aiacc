package cli

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/claude"
	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
	"github.com/spf13/cobra"
)

// runPicker drives the interactive front door shared by a bare `aiacc` and
// `aiacc use` with no account. filter scopes the list to one provider.
//
// Poka-yoke: if the shell hook is not active, switching cannot work — so instead
// of a picker that silently no-ops, we show the one-time setup gate. The single
// confusing state ("I switched and nothing happened") is unreachable.
func runPicker(cmd *cobra.Command, shellName, filter string) error {
	if !hookActive(cmd.OutOrStdout()) {
		return runSetupGate(shellName)
	}

	path, err := configPath()
	if err != nil {
		return err
	}
	for {
		c, err := config.Load(path)
		if err != nil {
			return err
		}
		rows := collectRows(c, filter)

		res, err := tui.Run(rows)
		if err != nil {
			return err
		}
		switch res.Kind {
		case tui.Cancelled:
			return nil // emit nothing — a hook eval of "" is a no-op
		case tui.Add:
			if err := addFromPicker(path); err != nil {
				return err
			}
			continue // rebuild the list and re-open
		case tui.Switch:
			row := rows[res.Index]
			line, err := shell.ExportLine(shellName, row.EnvVar, row.Dir)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), line)
			return nil
		}
	}
}

// runSetupGate shows the one-time hook-install screen and, on request, appends
// the hook line to the shell's startup file.
func runSetupGate(shellName string) error {
	sh := supportedShell(shellName)
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
		Line:           line,
		Path:           tildeize(rc),
		AlreadyPresent: fileContains(rc, line),
	}
	return tui.RunSetup(info, func() error { return installHook(rc, line) })
}

// installHook appends the hook line to rc, idempotently — an existing line is
// left untouched so re-running never duplicates it. The parent directory is
// created for fish's ~/.config/fish.
func installHook(rc, line string) error {
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
	_, err = fmt.Fprintf(f, "\n# aiacc shell hook\n%s\n", line)
	return err
}

func fileContains(path, needle string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), needle)
}

// supportedShell maps whatever $SHELL reports onto a shell aiacc can wire up,
// defaulting to bash so the gate always has something concrete to show.
func supportedShell(name string) string {
	switch name {
	case "bash", "zsh", "fish":
		return name
	default:
		return "bash"
	}
}

// tildeize replaces the home-dir prefix of p with ~ for display.
func tildeize(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// addFromPicker runs the existing line-based add wizard on /dev/tty. It must not
// touch cmd's stdout: that channel carries the export line the shell hook
// evaluates, and wizard prompts eval'd as shell would be a disaster.
func addFromPicker(cfgPath string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer tty.Close()
	fmt.Fprintln(tty)
	if err := runAddWizard(tty, tty, cfgPath); err != nil {
		fmt.Fprintf(tty, "add failed: %v\n", err)
	}
	fmt.Fprint(tty, "\npress enter to continue…")
	readLine(tty)
	return nil
}

func readLine(r io.Reader) {
	var b [1]byte
	for {
		n, err := r.Read(b[:])
		if n == 0 || err != nil || b[0] == '\n' {
			return
		}
	}
}

// hookActive reports whether aiacc is being run through its shell hook, which is
// what actually applies a switch. The hook captures stdout in a command
// substitution, so a non-terminal stdout means the hook is present; a terminal
// stdout means the export line would just be printed and never evaluated.
func hookActive(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return true // not a real terminal (tests, pipes): assume captured
	}
	return !isTerminal(f)
}

// isTerminal reports whether f is a character device (a tty), using only the
// stdlib: a pipe or regular file is not a char device.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// collectRows builds the picker rows from config: every account (optionally
// filtered to one provider), with its expanded dir, whether that dir exists, the
// provider's env var, the live-active flag, and logged-in identity. Accounts
// whose dir cannot be resolved at all are skipped; a merely missing dir is kept
// and shown blocked, so the user sees what to repair.
func collectRows(c *config.Config, filter string) []tui.Row {
	var rows []tui.Row
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		if filter != "" && pn != filter {
			continue
		}
		env, _ := provider.EnvVar(c, pn)
		live := ""
		if env != "" {
			live = os.Getenv(env)
		}
		p := c.Providers[pn]
		for _, an := range slices.Sorted(maps.Keys(p.Accounts)) {
			dir, err := provider.AccountDir(c, pn, an)
			if err != nil {
				continue
			}
			_, statErr := os.Stat(dir)
			rows = append(rows, tui.Row{
				Provider:  pn,
				Account:   an,
				Email:     claude.Detect(dir).Email,
				Dir:       dir,
				EnvVar:    env,
				Active:    env != "" && dir == live,
				DirExists: statErr == nil,
			})
		}
	}
	return rows
}
