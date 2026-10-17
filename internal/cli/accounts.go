package cli

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"text/tabwriter"

	"github.com/CarlosDanielDev/aiacc/internal/claude"
	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
	"github.com/spf13/cobra"
)

// configPath is indirected so tests can point it at a temp file.
var configPath = config.DefaultPath

// stdinIsTerminal is indirected so tests can drive both add surfaces.
var stdinIsTerminal = func() bool { return isTerminal(os.Stdin) }

func newAddCmd() *cobra.Command {
	var dir, envVar, command, settings string
	var quota int
	cmd := &cobra.Command{
		Use:   "add [provider] [account]",
		Short: "Register an account (interactive wizard when run with no arguments)",
		Long: "Register an account. Claude and Codex are built-in; any other CLI that " +
			"selects its config through an environment variable works too — give " +
			"--env <ENV_VAR> and --command <cli> the first time you add one for that provider.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				// Interactive terminal → the framed add screen — but only
				// when no flag needs applying: the screen has no field for
				// them, so they would be silently dropped. Piped/scripted
				// (and tests) → the line wizard, which reads stdin and lets
				// each given flag preempt its question.
				if stdinIsTerminal() {
					for _, flag := range []string{"dir", "settings", "env", "command", "quota"} {
						if cmd.Flags().Changed(flag) {
							return fmt.Errorf("--%s needs <provider> <account> arguments — "+
								"run \"aiacc add <provider> <account> --dir …\" for the flag form, "+
								"or \"aiacc add\" with no flags for the interactive screen", flag)
						}
					}
					return runAddTUI(path)
				}
				return runAddWizard(cmd.InOrStdin(), cmd.OutOrStdout(), path,
					addPrefill{Dir: dir, Settings: settings, Env: envVar, Command: command, Quota: quota})
			}
			if len(args) != 2 {
				return fmt.Errorf("give both <provider> and <account>, or no arguments for the wizard")
			}
			providerName, account := args[0], args[1]
			if dir == "" {
				return fmt.Errorf("--dir is required")
			}
			// Expand once, here: the dir the user typed (~/.claude-foo) must
			// not grow a literal "./~" tree in the working directory, and the
			// shared assets belong in the real profile. Registration keeps the
			// as-typed form — the stored paths re-expand at launch (LaunchArgs)
			// and the rename/add flows depend on the tilde surviving.
			realDir, err := provider.ExpandHome(dir)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(realDir, 0o755); err != nil {
				return err
			}
			if settings, err = resolveSettings(providerName, dir, settings); err != nil {
				return err
			}
			if err := saveAccount(path, providerName, account, dir, quota, envVar, command, settings); err != nil {
				return err
			}
			syncLauncher(path, providerName, account) // keep the command in sync
			linkProfile(providerName, realDir)        // same skills/agents as the bare CLI
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "config directory for this account (required unless using the wizard)")
	cmd.Flags().IntVar(&quota, "quota", 0, "optional manual plan size")
	cmd.Flags().StringVar(&envVar, "env", "", "config-dir env var for a custom (non-preset) provider")
	cmd.Flags().StringVar(&command, "command", "", "CLI to launch for a custom (non-preset) provider")
	cmd.Flags().StringVar(&settings, "settings", "", "settings file the launcher passes the CLI: a path, or an endpoint keyword (glm) that generates a skeleton (claude only)")
	return cmd
}

// settingsRefusal is the CLI's refusal of a --settings value that is neither a
// file path nor a keyword. It states the value once, then the known keywords,
// and unwraps to the shared refusal so errors.Is(err, provider.ErrUnknownEndpoint)
// still holds.
type settingsRefusal struct {
	value, keywords string
	cause           error
}

func (e settingsRefusal) Error() string {
	return fmt.Sprintf("--settings %q is neither a file path nor a known endpoint keyword (known: %s). "+
		"Give a settings file path instead; \"custom\" needs the interactive add screen (\"aiacc add\" with no arguments)",
		e.value, e.keywords)
}

func (e settingsRefusal) Unwrap() error { return e.cause }

// settingsEndpoint classifies a settings value without writing anything. It
// returns the endpoint and true when the value is a keyword whose skeleton
// resolveSettings generates; (zero, false, nil) when the value is registered
// verbatim; or the refusal resolveSettings would give. The keyword rule is the
// shared provider.ClassifySettings.
func settingsEndpoint(providerName, value string) (provider.Endpoint, bool, error) {
	if value == "" {
		return provider.Endpoint{}, false, nil
	}
	ep, keyword, err := provider.ClassifySettings(providerName, value)
	if err != nil {
		// err is the shared keyword refusal, worded for the add screen's hint
		// row. The CLI composes its own sentence from the refusal's parts.
		var kr provider.KeywordRefusal
		if !errors.As(err, &kr) {
			return provider.Endpoint{}, false, err
		}
		return provider.Endpoint{}, false, settingsRefusal{value: kr.Value, keywords: kr.Keywords, cause: err}
	}
	if !keyword {
		return provider.Endpoint{}, false, nil // not a keyword — today's path behavior, unchanged
	}
	if ep.BaseURL == "" {
		return provider.Endpoint{}, false, fmt.Errorf("--settings %s needs a base URL only the interactive add screen can ask for — "+
			"run \"aiacc add\" with no arguments and pick it there (\"glm\" works right on the command line)", value)
	}
	return ep, true, nil
}

// resolveSettings turns the --settings value into the path the account
// registers, the CLI spelling of the add screen's settings field. Through the
// shared classification rule (provider.ClassifySettings) an endpoint keyword
// (glm) generates the settings skeleton into the account dir and yields the
// generated path, exactly as if the user had written the file by hand and
// passed its path. A keyword whose preset has no base URL (custom) is
// refused: its base URL is the user's to type, and only the interactive
// add screen can ask for it. A bare word that names no keyword is refused
// with the valid ones listed — a typo must not silently register a bogus
// settings path — while anything path-like keeps today's verbatim path
// behavior. A file already at the target aborts the add with its path named;
// nothing is overwritten (the aiacc link rule). A refusal is decided by
// settingsEndpoint before anything is written.
func resolveSettings(providerName, dir, value string) (string, error) {
	ep, keyword, err := settingsEndpoint(providerName, value)
	if err != nil {
		return "", err
	}
	if !keyword {
		return value, nil
	}
	// The screen and the CLI register through the same sequence, so the
	// skeleton lands in the real profile while the registered path keeps the
	// as-typed form (stored paths re-expand at launch; see EnsureSkeleton).
	return provider.EnsureSkeleton(dir, ep)
}

// settingsFlagRefusal refuses a settings value for a provider whose CLI has no
// flag to load one. saveAccount applies it, and the wizard checks it before
// anything is written.
func settingsFlagRefusal(providerName, settings string) error {
	if settings != "" && provider.Presets[providerName].SettingsFlag == "" {
		return fmt.Errorf("%s has no settings flag, so --settings cannot apply to it", providerName)
	}
	return nil
}

// saveAccount registers (provider, account) at dir in the config file at path,
// filling the provider's env var and launch command from the flags, else the
// preset, else leaving them empty for the user to set. settings is an optional
// extra settings file, refused for a provider whose CLI has no flag to load one.
// Used by `add` and the wizard.
func saveAccount(path, providerName, account, dir string, quota int, envOverride, cmdOverride, settings string) error {
	if err := settingsFlagRefusal(providerName, settings); err != nil {
		return err
	}
	// Update holds the config file lock across load-modify-save, so a
	// concurrent add/remove cannot turn this into a lost update.
	return config.Update(path, func(c *config.Config) error {
		// The name is also the launcher filename (`binDir/<name>`) and a token
		// in every generated script, so a traversal or junk name is refused
		// here — the same rule the add screen enforces as you type.
		if !tui.ValidName(account) {
			return fmt.Errorf("invalid account name %q: names are limited to letters, digits, \"-\" and \"_\", starting with a letter or \"_\"", account)
		}
		env := envOverride
		if env == "" {
			env, _ = provider.EnvVar(c, providerName) // preset, else ""
		}
		cmd := cmdOverride
		if cmd == "" {
			cmd, _ = provider.Command(c, providerName) // preset, else ""
		}
		p := c.Providers[providerName]
		if p.Accounts == nil {
			p.Accounts = map[string]config.Account{}
		}
		if p.EnvVar == "" {
			p.EnvVar = env
		}
		if p.Command == "" {
			p.Command = cmd
		}
		p.Accounts[account] = config.Account{Dir: dir, Quota: quota, Settings: settings}
		c.Providers[providerName] = p
		return nil
	})
}

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <provider> <account>",
		Short: "Unregister an account (keeps the directory)",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			return removeAccount(path, args[0], args[1])
		},
	}
}

// removeAccount unregisters (provider, account) from the config at path. The
// account's directory is left on disk — only the registration is dropped. Used
// by both `remove` and the picker's `d`.
func removeAccount(path, providerName, account string) error {
	if err := config.Update(path, func(c *config.Config) error {
		if p, ok := c.Providers[providerName]; ok {
			delete(p.Accounts, account)
			c.Providers[providerName] = p
		}
		return nil
	}); err != nil {
		return err
	}
	removeLauncher(path, account) // drop the command too (best-effort)
	return nil
}

func newRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <provider> <old> <new>",
		Short: "Rename an account (and its launcher command)",
		Args:  cobra.ExactArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			if err := renameAccount(path, args[0], args[1], args[2]); err != nil {
				return err
			}
			removeLauncher(path, args[1])        // drop the old command
			syncLauncher(path, args[0], args[2]) // install the new one
			return nil
		},
	}
}

// renameAccount changes an account's name (which is also its launcher command),
// keeping its directory and quota. It refuses to overwrite an existing name, and
// swaps the launcher script so no stale command is left behind. Used by both
// `rename` and the picker's `r`.
func renameAccount(path, providerName, oldName, newName string) error {
	if newName == oldName {
		return nil
	}
	return config.Update(path, func(c *config.Config) error {
		// Same rule as saveAccount: the new name becomes the launcher filename.
		if !tui.ValidName(newName) {
			return fmt.Errorf("invalid account name %q: names are limited to letters, digits, \"-\" and \"_\", starting with a letter or \"_\"", newName)
		}
		p, ok := c.Providers[providerName]
		if !ok {
			return fmt.Errorf("unknown provider %q", providerName)
		}
		acct, ok := p.Accounts[oldName]
		if !ok {
			return fmt.Errorf("unknown account %q", oldName)
		}
		if _, exists := p.Accounts[newName]; exists {
			return fmt.Errorf("account %q already exists", newName)
		}
		p.Accounts[newName] = acct
		delete(p.Accounts, oldName)
		c.Providers[providerName] = p
		return nil
	})
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List providers and accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "PROVIDER\tACCOUNT\tLOGIN\tDIR")
			for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
				p := c.Providers[pn]
				for _, a := range slices.Sorted(maps.Keys(p.Accounts)) {
					login := "-"
					if exp, err := provider.AccountDir(c, pn, a); err == nil {
						if info := claude.Detect(exp); info.LoggedIn {
							login = info.Email
						}
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", pn, a, login, p.Accounts[a].Dir)
				}
			}
			return w.Flush()
		},
	}
}
