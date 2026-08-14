package cli

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"text/tabwriter"

	"github.com/CarlosDanielDev/aiacc/internal/claude"
	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/spf13/cobra"
)

// configPath is indirected so tests can point it at a temp file.
var configPath = config.DefaultPath

func newAddCmd() *cobra.Command {
	var dir string
	var quota int
	cmd := &cobra.Command{
		Use:   "add [provider] [account]",
		Short: "Register an account (interactive wizard when run with no arguments)",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				// Interactive terminal → the framed add screen. Piped/scripted
				// (and tests) → the line wizard, which reads stdin.
				if isTerminal(os.Stdin) {
					return runAddTUI(path)
				}
				return runAddWizard(cmd.InOrStdin(), cmd.OutOrStdout(), path)
			}
			if len(args) != 2 {
				return fmt.Errorf("give both <provider> and <account>, or no arguments for the wizard")
			}
			providerName, account := args[0], args[1]
			if dir == "" {
				return fmt.Errorf("--dir is required")
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			return saveAccount(path, providerName, account, dir, quota)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "config directory for this account (required unless using the wizard)")
	cmd.Flags().IntVar(&quota, "quota", 0, "optional manual plan size")
	return cmd
}

// saveAccount registers (provider, account) at dir in the config file at path,
// merging the provider's preset env var. Used by both `add` and the wizard.
func saveAccount(path, providerName, account, dir string, quota int) error {
	c, err := config.Load(path)
	if err != nil {
		return err
	}
	// An unknown provider with no preset gets an empty env_var; the user defines
	// it later (switching is where a missing one errors).
	env, _ := provider.EnvVar(c, providerName)
	p := c.Providers[providerName]
	if p.Accounts == nil {
		p.Accounts = map[string]config.Account{}
	}
	if p.EnvVar == "" {
		p.EnvVar = env
	}
	p.Accounts[account] = config.Account{Dir: dir, Quota: quota}
	c.Providers[providerName] = p
	return config.Save(path, c)
}

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <provider> <account>",
		Short: "Unregister an account (keeps the directory)",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			providerName, account := args[0], args[1]
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			if p, ok := c.Providers[providerName]; ok {
				delete(p.Accounts, account)
				c.Providers[providerName] = p
			}
			return config.Save(path, c)
		},
	}
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
