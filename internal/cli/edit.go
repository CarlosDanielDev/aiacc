package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
)

// editScreen is indirected so tests can drive the picker's edit flow without a
// tty.
var editScreen = tui.RunEdit

// runEditTUI is the picker's `e`: the edit screen for a profile — its
// registration (the dir and the settings link, prefilled with the stored values
// as typed) and, when the settings file is readable, the file's env block.
// Enter saves the registration through the add path's validations and then the
// file; a refusal is the hint row and nothing is written. A profile with no
// settings file, or an unreadable one, still edits: only the file's rows are
// absent.
func runEditTUI(cfgPath string, row tui.Row) error {
	c, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	stored := c.Providers[row.Provider].Accounts[row.Account]
	form := tui.EditForm{Dir: stored.Dir, Settings: stored.Settings}
	if row.Settings != "" {
		if env, err := provider.ReadSettingsEnv(row.Settings); err == nil {
			form.SettingsEnv, form.File = env, true
		}
	}
	var launchErr error
	_, err = editScreen(row.Account, row.Settings, form, func(dir, settings string) error {
		if err := saveRegistration(cfgPath, row.Provider, row.Account, dir, settings); err != nil {
			return err
		}
		launchErr = syncLauncher(cfgPath, row.Provider, row.Account)
		return nil
	})
	if err != nil {
		return err
	}
	return launcherNotice(launchErr)
}

// saveRegistration applies the edit screen's Enter to the registration: the dir
// and the settings link, through the same validations the add path uses. The
// settings value resolves exactly as add resolves it (resolveSettings: a path
// is stored as typed once it is not a directory, a keyword generates its
// skeleton into the new dir), the dir is created and given the shared links,
// and the config is rewritten under its lock. The name never changes here, so
// the add path's name-collision checks have no edit counterpart. An unchanged
// registration is a no-op: a keyword's skeleton already exists, and
// regenerating it would refuse on it.
func saveRegistration(cfgPath, providerName, account, dir, settings string) error {
	if err := settingsFlagRefusal(providerName, settings); err != nil {
		return err
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	stored, ok := c.Providers[providerName].Accounts[account]
	if !ok {
		return fmt.Errorf("unknown account %q under %s", account, providerName)
	}
	if dir == stored.Dir && settings == stored.Settings {
		return nil
	}
	// A keyword that already resolves to the stored skeleton is a no-op too:
	// the screen keeps the typed value across a refused file save, and a second
	// Enter must not turn into a refusal on the existing file. A no-op it is
	// only while the skeleton still exists — a file that has since disappeared
	// is exactly what re-typing the keyword regenerates.
	if dir == stored.Dir && settings != stored.Settings {
		if ep, keyword, err := provider.ClassifySettings(providerName, settings); err == nil && keyword &&
			filepath.Join(dir, ep.Filename) == stored.Settings {
			if real, err := provider.ExpandHome(stored.Settings); err == nil {
				if _, statErr := os.Stat(real); statErr == nil {
					return nil
				}
			}
		}
	}
	resolved, err := resolveSettings(providerName, dir, settings)
	if err != nil {
		return err
	}
	realDir, err := provider.ExpandHome(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		return err
	}
	if err := updateRegistration(cfgPath, providerName, account, dir, resolved); err != nil {
		return err
	}
	linkProfile(providerName, realDir) // same skills/agents as the bare CLI
	return nil
}
