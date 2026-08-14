package cli

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"

	"github.com/CarlosDanielDev/aiacc/internal/claude"
	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
	"github.com/CarlosDanielDev/aiacc/internal/usage"
	"github.com/spf13/cobra"
)

// runPicker drives the interactive TUI shared by a bare `aiacc` and `aiacc use`
// with no account. filter scopes the list to one provider ("" = all).
//
// It loops so the add wizard (the `a` key) can register an account and return
// to a freshly-rebuilt list without leaving the TUI. On a chosen switch it
// prints the export line to cmd's stdout — which the shell hook captures and
// evaluates. If the hook is not active (stdout is a terminal, not a pipe) the
// TUI has already shown its andon; we still print the line so it is not lost.
func runPicker(cmd *cobra.Command, shellName, filter string) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	hookActive := hookActive(cmd.OutOrStdout())

	for {
		c, err := config.Load(path)
		if err != nil {
			return err
		}
		rows := collectRows(c, filter)

		res, err := tui.Run(rows, hookActive)
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

// addFromPicker runs the existing line-based add wizard on /dev/tty. It must not
// touch cmd's stdout: that channel is reserved for the export line the shell
// hook evaluates, and wizard prompts eval'd as shell would be a disaster.
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
// provider's env var, the live-active flag, logged-in identity, token total, and
// quota. Accounts whose dir cannot be resolved at all are skipped; a merely
// missing dir is kept and shown blocked, so the user sees what to repair.
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
			t, _ := usage.Aggregate(dir)
			rows = append(rows, tui.Row{
				Provider:  pn,
				Account:   an,
				Email:     claude.Detect(dir).Email,
				Dir:       dir,
				EnvVar:    env,
				Active:    env != "" && dir == live,
				DirExists: statErr == nil,
				Tokens:    t.Total(),
				Quota:     p.Accounts[an].Quota,
			})
		}
	}
	return rows
}
