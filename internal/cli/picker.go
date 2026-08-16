package cli

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/CarlosDanielDev/aiacc/internal/claude"
	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
)

// runPicker drives the interactive profile launcher (a bare `aiacc`). filter
// scopes the list to one provider ("" = all). It loops so add/remove return to a
// freshly-rebuilt list; Launch replaces this process with the provider's CLI.
func runPicker(filter string) error {
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

		res, err := tui.Run(rows, setupNeeded())
		if err != nil {
			return err
		}
		switch res.Kind {
		case tui.Cancelled:
			return nil
		case tui.Add:
			if err := runAddTUI(path); err != nil {
				return err
			}
			continue
		case tui.Rename:
			row := rows[res.Index]
			var taken []string
			for j, r := range rows {
				if j != res.Index {
					taken = append(taken, r.Account)
				}
			}
			newName, err := tui.RunRename(row.Account, taken)
			if err != nil {
				return err
			}
			if newName != "" {
				if err := renameAccount(path, row.Provider, row.Account, newName); err != nil {
					return err
				}
				removeLauncher(row.Account)               // drop old command
				syncLauncher(path, row.Provider, newName) // install new one
			}
			continue
		case tui.Handoff:
			row := rows[res.Index]
			if err := runHandoffTUI(path, row.Provider, row.Account); err != nil {
				return err
			}
			continue
		case tui.Setup:
			res, err := doSetup(path)
			if err != nil {
				return err
			}
			if err := tui.RunSetupResult(res); err != nil {
				return err
			}
			continue
		case tui.Remove:
			row := rows[res.Index]
			if err := removeAccount(path, row.Provider, row.Account); err != nil {
				return err
			}
			continue
		case tui.Launch:
			return launchProfile(rows[res.Index]) // execs; returns only on error
		}
	}
}

// launchProfile replaces the aiacc process with the profile's CLI, its env var
// pointed at the profile's config dir. On success it never returns (the terminal
// is handed to the launched program); it returns only if the command is missing.
func launchProfile(row tui.Row) error {
	bin, err := exec.LookPath(row.Command)
	if err != nil {
		return fmt.Errorf("%s not found in PATH — is it installed?", row.Command)
	}
	env := setEnv(os.Environ(), row.EnvVar, row.Dir)
	return syscall.Exec(bin, []string{row.Command}, env)
}

// setEnv returns env with key set to val, replacing any existing entry.
func setEnv(env []string, key, val string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return append(out, key+"="+val)
}

// launchCommand is the CLI aiacc runs for a provider. Claude Code is the built-in
// launcher; other providers have none yet (their rows stay in the list but can't
// be launched until one is added).
func launchCommand(providerName string) string {
	if providerName == "claude" {
		return "claude"
	}
	return ""
}

// runAddTUI shows the framed add screen and, on confirm, creates the config dir
// and registers the profile. The screen constrains the name to a safe launcher
// command, so a junk name can't be entered.
func runAddTUI(cfgPath string) error {
	res, err := tui.RunAdd(currentClaudeLogin())
	if err != nil {
		return err
	}
	if !res.OK {
		return nil
	}
	dir := expandTilde(res.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := saveAccount(cfgPath, "claude", res.Name, res.Dir, 0); err != nil {
		return err
	}
	syncLauncher(cfgPath, "claude", res.Name) // keep the command in sync (best-effort)
	return nil
}

// currentClaudeLogin is the email (and org) logged into the active Claude dir,
// shown in the add screen for context, or "" when not logged in.
func currentClaudeLogin() string {
	cur := currentClaudeDir()
	if cur == "" {
		return ""
	}
	info := claude.Detect(cur)
	if !info.LoggedIn {
		return ""
	}
	if info.Organization != "" {
		return info.Email + " (" + info.Organization + ")"
	}
	return info.Email
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
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

// collectRows builds the launcher rows from config: every profile (optionally
// filtered to one provider), with its expanded dir, whether that dir exists, the
// provider's env var and launch command, and the logged-in identity. Profiles
// whose dir can't be resolved at all are skipped; a merely missing dir is kept
// and shown blocked, so the user sees what to repair or remove.
func collectRows(c *config.Config, filter string) []tui.Row {
	var rows []tui.Row
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		if filter != "" && pn != filter {
			continue
		}
		env, _ := provider.EnvVar(c, pn)
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
				DirExists: statErr == nil,
				EnvVar:    env,
				Command:   launchCommand(pn),
			})
		}
	}
	return rows
}
