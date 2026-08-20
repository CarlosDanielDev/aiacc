package cli

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Install the launcher commands (one step — no shell reload needed)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			res, err := doSetup(path)
			if err != nil {
				return err
			}
			// Interactive terminal → the framed result screen; piped/scripted →
			// plain, greppable output.
			if isTerminal(os.Stdout) {
				return tui.RunSetupResult(res)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed %d command(s) to %s:\n", len(res.Names), res.BinDir)
			for _, n := range res.Names {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", n)
			}
			if res.WorksNow {
				fmt.Fprintf(cmd.OutOrStdout(), "They work now — try: %s\n", res.Example)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Added %s to your PATH — open a new terminal, then: %s\n", res.BinDir, res.Example)
			}
			return nil
		},
	}
}

// doSetup is the one-step install: it writes an executable launcher for every
// account into a directory on PATH, so the commands work immediately with no
// sourcing or shell reload. It only touches your shell config in the rare case
// that no writable directory is already on PATH.
func doSetup(cfgPath string) (tui.SetupResult, error) {
	c, err := config.Load(cfgPath)
	if err != nil {
		return tui.SetupResult{}, err
	}
	binDir, onPath := launcherBinDir()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return tui.SetupResult{}, err
	}
	names, err := writeAllLaunchers(c, binDir)
	if err != nil {
		return tui.SetupResult{}, err
	}
	if !onPath {
		// Fallback: make binDir reachable next shell. Best-effort — the scripts
		// are already written, so a manual PATH add still works if this can't.
		ensureOnPath(binDir)
	}
	example := "aiacc"
	if len(names) > 0 {
		example = names[0]
	}
	return tui.SetupResult{
		BinDir:   tildeize(binDir),
		Names:    names,
		Example:  example,
		WorksNow: onPath,
	}, nil
}

// writeAllLaunchers writes one executable per launchable account into binDir and
// returns the command names, sorted.
func writeAllLaunchers(c *config.Config, binDir string) ([]string, error) {
	var names []string
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		env, _ := provider.EnvVar(c, pn)
		cmd := launchCommand(c, pn)
		if cmd == "" || env == "" {
			continue
		}
		for _, an := range slices.Sorted(maps.Keys(c.Providers[pn].Accounts)) {
			dir, err := provider.AccountDir(c, pn, an)
			if err != nil {
				continue
			}
			if err := writeLauncher(binDir, an, cmd, env, dir); err != nil {
				return names, err
			}
			names = append(names, an)
		}
	}
	return names, nil
}

// writeLauncher writes a single executable launcher script.
func writeLauncher(binDir, name, command, envVar, dir string) error {
	content, err := shell.LauncherScript(command, envVar, dir)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(binDir, name), []byte(content), 0o755)
}

// syncLauncher (re)writes one account's launcher if setup has been run (the bin
// dir exists), so `add` keeps the commands current without a manual re-setup.
// Best-effort: a failure here never fails the add.
func syncLauncher(cfgPath, providerName, account string) {
	binDir, _ := launcherBinDir()
	if info, err := os.Stat(binDir); err != nil || !info.IsDir() {
		return // not set up yet; `aiacc setup` will create it
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		return
	}
	cmd := launchCommand(c, providerName)
	env, _ := provider.EnvVar(c, providerName)
	dir, err := provider.AccountDir(c, providerName, account)
	if err != nil || cmd == "" || env == "" {
		return
	}
	_ = writeLauncher(binDir, account, cmd, env, dir)
}

// removeLauncher deletes an account's launcher script if present. Best-effort.
func removeLauncher(account string) {
	binDir, _ := launcherBinDir()
	_ = os.Remove(filepath.Join(binDir, account))
}

// launcherBinDir picks where to install the launchers and whether it is already
// on PATH. It prefers a writable directory already on PATH — a HOME-owned one
// first, then any other — so the commands work with no PATH change. If nothing on
// PATH is writable it falls back to ~/.local/bin (created, added to PATH).
func launcherBinDir() (string, bool) {
	home, _ := os.UserHomeDir()
	preferred := []string{filepath.Join(home, ".local", "bin"), filepath.Join(home, "bin")}
	pathDirs := filepath.SplitList(os.Getenv("PATH"))

	// 1. A preferred HOME dir already on PATH and writable.
	for _, d := range preferred {
		if slices.Contains(pathDirs, d) && writable(d) {
			return d, true
		}
	}
	// 2. Any writable dir on PATH — HOME-owned first, then the rest in order.
	var homeDirs, others []string
	for _, d := range pathDirs {
		if d != "" && strings.HasPrefix(d, home+string(os.PathSeparator)) {
			homeDirs = append(homeDirs, d)
		} else if d != "" {
			others = append(others, d)
		}
	}
	for _, d := range append(homeDirs, others...) {
		if writable(d) {
			return d, true
		}
	}
	// 3. Fallback: ~/.local/bin, not yet on PATH.
	return preferred[0], false
}

// writable reports whether dir is an existing directory we can create a file in.
func writable(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	probe := filepath.Join(dir, ".aiacc-writetest")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(probe)
	return true
}

// ensureOnPath adds binDir to PATH via the shell startup file, idempotently.
func ensureOnPath(binDir string) {
	sh := detectShell()
	line, err := shell.PathLine(sh, binDir)
	if err != nil {
		return
	}
	rc, err := shell.RcPath(sh)
	if err != nil {
		return
	}
	_ = appendLine(rc, line)
}

// setupNeeded reports whether the launcher commands aren't installed yet, so the
// picker can nudge toward `s`. True when there are launchable accounts but the
// bin dir has no launcher for the first one.
func setupNeeded() bool {
	path, err := configPath()
	if err != nil {
		return false
	}
	c, err := config.Load(path)
	if err != nil {
		return false
	}
	binDir, _ := launcherBinDir()
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		if launchCommand(c, pn) == "" {
			continue
		}
		for _, an := range slices.Sorted(maps.Keys(c.Providers[pn].Accounts)) {
			if _, err := os.Stat(filepath.Join(binDir, an)); err != nil {
				return true // at least one account has no launcher installed
			}
		}
	}
	return false
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

// appendLine adds line to rc, idempotently, creating the file/dir if needed.
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
	_, err = fmt.Fprintf(f, "\n# added by aiacc\n%s\n", line)
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
