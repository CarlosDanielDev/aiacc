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
	"text/tabwriter"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
	"github.com/CarlosDanielDev/aiacc/internal/usage"
	"github.com/spf13/cobra"
)

// configPath is indirected so tests can point it at a temp file.
var configPath = config.DefaultPath

// stdinIsTerminal is indirected so tests can drive both add surfaces.
var stdinIsTerminal = func() bool { return isTerminal(os.Stdin) }

func newAddCmd() *cobra.Command {
	var dir, envVar, command, settings string
	var baseURL, model, modelName string
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
					for _, flag := range []string{"dir", "settings", "env", "command", "quota", "base-url", "model", "model-name"} {
						if cmd.Flags().Changed(flag) {
							return fmt.Errorf("--%s needs <provider> <account> arguments — "+
								"run \"aiacc add <provider> <account> --dir …\" for the flag form, "+
								"or \"aiacc add\" with no flags for the interactive screen", flag)
						}
					}
					return runAddTUI(path)
				}
				return runAddWizard(cmd.InOrStdin(), cmd.OutOrStdout(), path,
					addPrefill{Dir: dir, Settings: settings, Env: envVar, Command: command, Quota: quota,
						BaseURL: baseURL, Model: model, ModelName: modelName})
			}
			if len(args) != 2 {
				return fmt.Errorf("give both <provider> and <account>, or no arguments for the wizard")
			}
			providerName, account := args[0], args[1]
			if dir == "" {
				return fmt.Errorf("--dir is required")
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			if err := checkNewName(cfg, providerName, account); err != nil {
				return err
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
			// Every settings refusal is decided before the dir is created, so a
			// refused add leaves nothing on disk. The order is the one the add
			// path always had — the settings value, then the provider's flag —
			// with custom's own checks after both.
			ep, custom := customPreset(providerName, settings)
			if !custom {
				if _, _, err := settingsEndpoint(providerName, settings); err != nil {
					return err
				}
			}
			if err := settingsFlagRefusal(providerName, settings); err != nil {
				return err
			}
			if custom {
				var missing []string
				if baseURL == "" {
					missing = append(missing, "--base-url")
				}
				if model == "" {
					missing = append(missing, "--model")
				}
				if len(missing) > 0 {
					return fmt.Errorf("--settings custom is missing %s (the endpoint's base URL and model id)",
						strings.Join(missing, " and "))
				}
				if ep, err = provider.FillEndpoint(ep, baseURL, model, modelName); err != nil {
					return err
				}
			} else {
				var given []string
				for _, flag := range []string{"base-url", "model", "model-name"} {
					if cmd.Flags().Changed(flag) {
						given = append(given, "--"+flag)
					}
				}
				if err := endpointFlagsRefusal(settings, given); err != nil {
					return err
				}
			}
			if err := os.MkdirAll(realDir, 0o755); err != nil {
				return err
			}
			if custom {
				settings, err = provider.EnsureSkeleton(dir, ep)
			} else {
				settings, err = resolveSettings(providerName, dir, settings)
			}
			if err != nil {
				return err
			}
			if err := saveAccount(path, providerName, account, dir, quota, envVar, command, settings); err != nil {
				return err
			}
			if err := syncLauncher(path, providerName, account); err != nil { // keep the command in sync
				fmt.Fprintln(cmd.ErrOrStderr(), "aiacc:", err)
			}
			linkProfile(providerName, realDir) // same skills/agents as the bare CLI
			if custom {
				printKeyHint(cmd.OutOrStdout(), realDir, ep)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "config directory for this account (required unless using the wizard)")
	cmd.Flags().IntVar(&quota, "quota", 0, "optional manual plan size")
	cmd.Flags().StringVar(&envVar, "env", "", "config-dir env var for a custom (non-preset) provider")
	cmd.Flags().StringVar(&command, "command", "", "CLI to launch for a custom (non-preset) provider")
	cmd.Flags().StringVar(&settings, "settings", "", "settings file the launcher passes the CLI: a path, or an endpoint keyword (glm, custom) that generates a skeleton (claude only)")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "with --settings custom: the Anthropic-compatible endpoint's base URL (https)")
	cmd.Flags().StringVar(&model, "model", "", "with --settings custom: the endpoint's model id")
	cmd.Flags().StringVar(&modelName, "model-name", "", "with --settings custom: the model's display name (default: the model id)")
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
		"Give a settings file path instead; \"custom\" takes --base-url and --model (or pick it in \"aiacc add\" with no arguments)",
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
		// Today's path behavior, unchanged, except a directory is refused here,
		// before any caller creates the account dir.
		if err := provider.RefuseSettingsDir(value); err != nil {
			return provider.Endpoint{}, false, err
		}
		return provider.Endpoint{}, false, nil
	}
	if ep.BaseURL == "" {
		// Unreachable from the add paths, which fill custom through customPreset.
		return provider.Endpoint{}, false, fmt.Errorf("--settings %s needs --base-url and --model", value)
	}
	return ep, true, nil
}

// resolveSettings turns the --settings value into the path the account
// registers, the CLI spelling of the add screen's settings field. Through the
// shared classification rule (provider.ClassifySettings) an endpoint keyword
// (glm) generates the settings skeleton into the account dir and yields the
// generated path, exactly as if the user had written the file by hand and
// passed its path. A keyword whose preset has no base URL (custom) is
// refused here: the add paths fill it from --base-url/--model (or the
// wizard's answers) through customPreset and provider.FillEndpoint and never
// pass it in. A bare word that names no keyword is refused
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
		return value, nil // settingsEndpoint has already refused a directory
	}
	// The screen and the CLI register through the same sequence, so the
	// skeleton lands in the real profile while the registered path keeps the
	// as-typed form (stored paths re-expand at launch; see EnsureSkeleton).
	return provider.EnsureSkeleton(dir, ep)
}

// customPreset reports whether a settings value names the endpoint keyword
// whose base URL is the user's to give (custom), and returns its preset for
// provider.FillEndpoint to fill from --base-url/--model/--model-name.
func customPreset(providerName, settings string) (provider.Endpoint, bool) {
	ep, keyword, err := provider.ClassifySettings(providerName, settings)
	return ep, err == nil && keyword && ep.BaseURL == ""
}

// endpointFlagsRefusal refuses the endpoint flags given with a settings value
// other than custom: they would otherwise be silently ignored.
func endpointFlagsRefusal(settings string, given []string) error {
	if len(given) == 0 {
		return nil
	}
	got := "no --settings"
	if settings != "" {
		got = "--settings " + settings
	}
	return fmt.Errorf("only --settings custom takes %s (got %s)", strings.Join(given, ", "), got)
}

// printKeyHint names the generated custom skeleton the user pastes the API key
// into. aiacc never asks for, reads or writes the key itself (ADR-0007).
func printKeyHint(out io.Writer, realDir string, ep provider.Endpoint) {
	fmt.Fprintf(out, "Paste your API key over %s in %s\n", provider.PlaceholderToken, filepath.Join(realDir, ep.Filename))
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
			return invalidNameError(account)
		}
		if owner, taken := ownerOf(c, account); taken {
			return nameTakenError(account, providerName, owner)
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

// accountOwners lists every provider that registers account, sorted. The
// launcher is named by the bare account, so one name should have one owner.
func accountOwners(c *config.Config, account string) []string {
	var out []string
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		if _, ok := c.Providers[pn].Accounts[account]; ok {
			out = append(out, pn)
		}
	}
	return out
}

// ownerOf reports the provider that already registers account, if any.
func ownerOf(c *config.Config, account string) (string, bool) {
	owners := accountOwners(c, account)
	if len(owners) == 0 {
		return "", false
	}
	return owners[0], true
}

// nameTakenError says why provider cannot register account: owner already has it.
// The name is one launcher file shared by every provider, so the way out is a
// different name, or removing the current holder first.
func nameTakenError(account, provider, owner string) error {
	if owner == provider {
		return fmt.Errorf("account %q already exists under %s: pick another name, or remove it first with aiacc remove %s %s", account, owner, owner, account)
	}
	return fmt.Errorf("account %q already exists under %s, and launchers are named by account, so %s cannot use it too: pick another name, or remove it first with aiacc remove %s %s", account, owner, provider, owner, account)
}

// invalidNameError refuses a name that cannot be a launcher filename.
func invalidNameError(name string) error {
	return fmt.Errorf("invalid account name %q: names are limited to letters, digits, \"-\" and \"_\", starting with a letter or \"_\"", name)
}

// checkNewName refuses an account name that is invalid, or that any provider
// already registers. It writes nothing, so the add entry points call it before
// they create a config dir or a settings skeleton: a refused add must leave no
// trace on disk. saveAccount repeats the checks under the config lock, for a
// concurrent writer.
func checkNewName(c *config.Config, providerName, account string) error {
	if !tui.ValidName(account) {
		return invalidNameError(account)
	}
	if owner, taken := ownerOf(c, account); taken {
		return nameTakenError(account, providerName, owner)
	}
	return nil
}

// checkNameOnDisk runs checkNewName against the config as it is on disk now. A
// caller that was interactive after it loaded the config must use this: another
// aiacc process may have registered the name in the meantime, and the dir must
// not be created on the strength of a stale snapshot.
func checkNameOnDisk(cfgPath, providerName, account string) error {
	c, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	return checkNewName(c, providerName, account)
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
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			if err := renameAccount(path, args[0], args[1], args[2]); err != nil {
				return err
			}
			removeLauncher(path, args[1])                                // drop the old command
			if err := syncLauncher(path, args[0], args[2]); err != nil { // install the new one
				fmt.Fprintln(cmd.ErrOrStderr(), "aiacc:", err)
			}
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
			return invalidNameError(newName)
		}
		p, ok := c.Providers[providerName]
		if !ok {
			return fmt.Errorf("unknown provider %q", providerName)
		}
		acct, ok := p.Accounts[oldName]
		if !ok {
			return fmt.Errorf("unknown account %q", oldName)
		}
		if owner, taken := ownerOf(c, newName); taken {
			return nameTakenError(newName, providerName, owner)
		}
		p.Accounts[newName] = acct
		delete(p.Accounts, oldName)
		c.Providers[providerName] = p
		return nil
	})
}

// accountColumns picks the optional column groups of the account table. list
// composes them from its flags; the hidden status and usage aliases each keep
// one fixed set, so their output is what it was before they shared this code.
type accountColumns struct {
	login  bool // LOGIN and DIR
	active bool // ACTIVE and LAST-USED
	usage  bool // INPUT, OUTPUT, TOTAL and USED/QUOTA: walks session logs
}

func newListCmd() *cobra.Command {
	var status, usage bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List providers and accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAccountTable(cmd, "", accountColumns{login: true, active: status, usage: usage})
		},
	}
	cmd.Flags().BoolVar(&status, "status", false, "add the active config dir and last-used time")
	cmd.Flags().BoolVar(&usage, "usage", false, "add token totals and USED/QUOTA (reads session logs: slow on large histories)")
	return cmd
}

// newStatusCmd and newUsageCmd are hidden aliases of `list --status` and
// `list --usage`, kept so existing scripts keep working.
func newStatusCmd() *cobra.Command {
	return newAccountAlias("status", "Show active account per provider", cobra.NoArgs, accountColumns{active: true})
}

func newUsageCmd() *cobra.Command {
	return newAccountAlias("usage [provider]", "Show token usage per account", cobra.MaximumNArgs(1), accountColumns{usage: true})
}

func newAccountAlias(use, short string, args cobra.PositionalArgs, cols accountColumns) *cobra.Command {
	return &cobra.Command{
		Use:    use,
		Short:  short,
		Hidden: true,
		Args:   args,
		RunE: func(cmd *cobra.Command, a []string) error {
			filter := ""
			if len(a) == 1 {
				filter = a[0]
			}
			return runAccountTable(cmd, filter, cols)
		},
	}
}

// runAccountTable loads the config and prints the account table to cmd's output.
func runAccountTable(cmd *cobra.Command, filter string, cols accountColumns) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	c, err := config.Load(path)
	if err != nil {
		return err
	}
	return printAccounts(cmd.OutOrStdout(), c, filter, cols)
}

// aggregateUsage is indirected so tests can count session-log walks.
var aggregateUsage = usage.Aggregate

// printAccounts writes one row per account of provider filter ("" = all). A row
// whose dir cannot resolve is dropped only when a column needs the dir.
func printAccounts(w io.Writer, c *config.Config, filter string, cols accountColumns) error {
	header := []string{"PROVIDER", "ACCOUNT"}
	if cols.login {
		header = append(header, "LOGIN", "DIR")
	}
	if cols.active {
		header = append(header, "ACTIVE", "LAST-USED")
	}
	if cols.usage {
		header = append(header, "INPUT", "OUTPUT", "TOTAL", "USED/QUOTA")
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		if filter != "" && pn != filter {
			continue
		}
		p := c.Providers[pn]
		env, _ := provider.EnvVar(c, pn)
		for _, a := range slices.Sorted(maps.Keys(p.Accounts)) {
			dir, err := provider.AccountDir(c, pn, a)
			if err != nil && (cols.active || cols.usage) {
				continue
			}
			cells := []string{pn, a}
			if cols.login {
				login := "-"
				if err == nil {
					if email, _, ok := loginOf(dir); ok {
						login = email
					}
				}
				cells = append(cells, login, p.Accounts[a].Dir)
			}
			if cols.active {
				active := ""
				if env != "" && os.Getenv(env) == dir {
					active = "*"
				}
				lastUsed := "-"
				if fi, err := os.Stat(dir); err == nil {
					lastUsed = fi.ModTime().Format("2006-01-02 15:04")
				}
				cells = append(cells, active, lastUsed)
			}
			if cols.usage {
				t, _ := aggregateUsage(dir)
				usedQuota := "-"
				if quota := p.Accounts[a].Quota; quota > 0 {
					usedQuota = fmt.Sprintf("%d/%d", t.Total(), quota)
				}
				cells = append(cells, fmt.Sprint(t.Input), fmt.Sprint(t.Output), fmt.Sprint(t.Total()), usedQuota)
			}
			fmt.Fprintln(tw, strings.Join(cells, "\t"))
		}
	}
	return tw.Flush()
}
