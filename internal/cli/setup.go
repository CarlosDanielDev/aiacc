package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/share"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	var replace bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install the launcher commands (one step — no shell reload needed)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			res, err := doSetup(path, replace)
			if err != nil {
				return err
			}
			// Interactive terminal → the framed result screen; piped/scripted →
			// plain, greppable output.
			if isTerminal(os.Stdout) {
				return tui.RunSetupResult(res)
			}
			writeSetupReport(cmd.OutOrStdout(), res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&replace, "replace", false, "take over a launcher path a file aiacc did not write (the original kept as <name>.aiacc-bak, or .aiacc-bak2, … when one already exists)")
	return cmd
}

// writeSetupReport is the plain-text outcome of setup for pipes and scripts.
func writeSetupReport(w io.Writer, res tui.SetupResult) {
	fmt.Fprintf(w, "Installed %d command(s) to %s:\n", len(res.Names), res.BinDir)
	for _, n := range res.Names {
		fmt.Fprintf(w, "  %s\n", n)
	}
	if res.Shared > 0 {
		fmt.Fprintf(w, "Shared your skills, sub-agents, commands and hooks with %d profile(s).\n", res.Shared)
	}
	if res.Conflicts > 0 {
		fmt.Fprintf(w, "%d entry(s) left alone (a profile owns them) — `aiacc link --replace` takes them over.\n", res.Conflicts)
	}
	for _, p := range res.Skipped {
		fmt.Fprintf(w, "warning: %s\n", foreignLauncher(p).Error())
	}
	for _, b := range res.Backups {
		fmt.Fprintf(w, "Original file kept as %s\n", tildeize(b))
	}
	if res.WorksNow {
		fmt.Fprintf(w, "They work now — try: %s\n", res.Example)
	} else {
		fmt.Fprintf(w, "Added %s to your PATH — open a new terminal, then: %s\n", res.BinDir, res.Example)
	}
}

// doSetup is the one-step install: it writes an executable launcher for every
// account into a directory on PATH, so the commands work immediately with no
// sourcing or shell reload. It only touches your shell config in the rare case
// that no writable directory is already on PATH.
func doSetup(cfgPath string, replace bool) (tui.SetupResult, error) {
	c, err := config.Load(cfgPath)
	if err != nil {
		return tui.SetupResult{}, err
	}
	binDir, onPath := launcherBinDir()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return tui.SetupResult{}, err
	}
	var res tui.SetupResult
	if err := writeAllLaunchers(c, binDir, replace, &res); err != nil {
		return tui.SetupResult{}, err
	}
	// Record where the launchers went, so a later PATH change cannot make
	// remove, sync or doctor look in the wrong directory.
	if err := config.Update(cfgPath, func(c *config.Config) error {
		c.BinDir = binDir
		return nil
	}); err != nil {
		return tui.SetupResult{}, err
	}
	// A launcher that starts the CLI with none of your skills, sub-agents or
	// slash commands is only half installed — share them into every profile as
	// part of the same one step. Never destructive: an entry a profile owns is
	// reported, not replaced (`aiacc link --replace` opts into that).
	res.Shared, res.Conflicts = sharedSummary(c)
	if !onPath {
		// Fallback: make binDir reachable next shell. Best-effort — the scripts
		// are already written, so a manual PATH add still works if this can't.
		ensureOnPath(binDir)
	}
	res.BinDir = tildeize(binDir)
	res.WorksNow = onPath
	res.Example = "aiacc"
	if len(res.Names) > 0 {
		res.Example = res.Names[0]
	}
	return res, nil
}

// writeAllLaunchers writes one executable per launchable account into binDir,
// recording the installed names, the launcher paths it left alone and any
// backups in res. A foreign file at a launcher path is skipped, not an error.
func writeAllLaunchers(c *config.Config, binDir string, replace bool, res *tui.SetupResult) error {
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
			backup, err := writeLauncher(binDir, an, replace, cmd, env, dir, provider.LaunchArgs(c, pn, an)...)
			if backup != "" {
				res.Backups = append(res.Backups, backup) // kept even when the install then failed
			}
			var foreign foreignLauncher
			switch {
			case errors.As(err, &foreign):
				res.Skipped = append(res.Skipped, string(foreign))
				continue
			case err != nil:
				return err
			}
			res.Names = append(res.Names, an)
		}
	}
	return nil
}

// foreignLauncher is the refusal to write over a file at a launcher path that
// aiacc did not write. The string is the path.
type foreignLauncher string

func (f foreignLauncher) Error() string {
	return fmt.Sprintf("%s is not an aiacc launcher, so no launcher was written there; move it aside, or run `aiacc setup --replace` to take it over", tildeize(string(f)))
}

// installLauncher puts a finished launcher at its path. It is a variable so a
// test can see the install step, and so a failing install can be simulated.
var installLauncher = os.Rename

// writeLauncher writes a single executable launcher script. Something at the
// path that aiacc did not write is never overwritten: it returns a
// foreignLauncher, unless replace moves it aside first, to <name>.aiacc-bak (or
// .aiacc-bak2, …), and returns that backup's path. The script is staged in a
// temp file beside the target and renamed into place, so nothing is ever
// written through a symlink at the path: rename replaces the link itself.
func writeLauncher(binDir, name string, replace bool, command, envVar, dir string, args ...string) (backup string, err error) {
	content, err := shell.LauncherScript(command, envVar, dir, args...)
	if err != nil {
		return "", err
	}
	staged, err := stageLauncher(binDir, name, content)
	if err != nil {
		return "", err
	}
	defer os.Remove(staged) // a no-op once the rename has moved it
	path := filepath.Join(binDir, name)
	if isForeign(path) {
		if !replace {
			return "", foreignLauncher(path)
		}
		backup = share.BackupName(path)
		if err := os.Rename(path, backup); err != nil {
			return "", err
		}
	}
	if err := installLauncher(staged, path); err != nil {
		if backup != "" {
			return backup, fmt.Errorf("%s was moved to %s, but its launcher was not installed: %w", tildeize(path), tildeize(backup), err)
		}
		return "", err
	}
	return backup, nil
}

// stageLauncher writes content to a new, uniquely named executable file in
// binDir and returns its path.
func stageLauncher(binDir, name, content string) (string, error) {
	f, err := os.CreateTemp(binDir, "."+name+".tmp-*")
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(content)
	if werr == nil {
		werr = f.Chmod(0o755)
	}
	if err := errors.Join(werr, f.Close()); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// isAiaccLauncher reports whether path is a launcher aiacc wrote: a small
// regular file that shell.IsLauncher accepts. Lstat, so a symlink never counts.
func isAiaccLauncher(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxLauncherSize {
		return false
	}
	body, err := os.ReadFile(path)
	return err == nil && shell.IsLauncher(string(body))
}

// isForeign reports whether something sits at path that aiacc did not write.
func isForeign(path string) bool {
	if _, err := os.Lstat(path); err != nil {
		return false // nothing there
	}
	return !isAiaccLauncher(path)
}

// syncLauncher (re)writes one account's launcher so `add` keeps the commands
// current without a manual re-setup. A config that was never set up has no
// launcher dir and is skipped silently: `aiacc setup` creates it. A recorded dir
// that has since disappeared is an error, not a skip, and nothing is written
// elsewhere: the launcher would land in a dir setup did not choose. Callers
// report the error; it never fails the add itself.
func syncLauncher(cfgPath, providerName, account string) error {
	c, err := config.Load(cfgPath)
	if err != nil {
		return nil
	}
	binDir := setupBinDir(c)
	if info, err := os.Stat(binDir); err != nil || !info.IsDir() {
		if c.BinDir != "" {
			return fmt.Errorf("%s is gone, so %s has no launcher; run `aiacc setup` to reinstall the commands", tildeize(binDir), account)
		}
		return nil // never set up; `aiacc setup` will create the dir
	}
	cmd := launchCommand(c, providerName)
	env, _ := provider.EnvVar(c, providerName)
	dir, err := provider.AccountDir(c, providerName, account)
	if err != nil || cmd == "" || env == "" {
		return nil
	}
	_, err = writeLauncher(binDir, account, false, cmd, env, dir, provider.LaunchArgs(c, providerName, account)...)
	return err
}

// removeLauncher deletes an account's launcher script if present. Best-effort. A
// file aiacc did not write stays: removing an account never deletes your script.
func removeLauncher(cfgPath, account string) {
	c, err := config.Load(cfgPath)
	if err != nil {
		return
	}
	if path := filepath.Join(setupBinDir(c), account); isAiaccLauncher(path) {
		_ = os.Remove(path)
	}
}

// setupBinDir is the directory setup installed the launchers into, as recorded
// in the config. A config that predates the record falls back to the directory
// PATH points at today, which is how it was found before the record existed.
func setupBinDir(c *config.Config) string {
	if c.BinDir != "" {
		return c.BinDir
	}
	binDir, _ := launcherBinDir()
	return binDir
}

// launcherBinDir picks where to install the launchers and whether it is already
// on PATH. It prefers a writable directory already on PATH — a HOME-owned one
// first, then any other — so the commands work with no PATH change. If nothing on
// PATH is writable it falls back to ~/.local/bin (created, added to PATH).
func launcherBinDir() (string, bool) {
	home, _ := os.UserHomeDir()
	preferred := userBinDirs(home)
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
	binDir := setupBinDir(c)
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

// userBinDirs are the HOME bin dirs launchers prefer, in order.
func userBinDirs(home string) []string {
	return []string{filepath.Join(home, ".local", "bin"), filepath.Join(home, "bin")}
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
