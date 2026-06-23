package cli

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

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
		Use:   "add <provider> <account>",
		Short: "Register an account",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			providerName, account := args[0], args[1]
			if dir == "" {
				return fmt.Errorf("--dir is required")
			}
			env, err := provider.EnvVar(&config.Config{Providers: map[string]config.Provider{}}, providerName)
			if err != nil && providerName != "" {
				env = "" // unknown provider with no preset; user must define env via config later
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
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
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "config directory for this account (required)")
	cmd.Flags().IntVar(&quota, "quota", 0, "optional manual plan size")
	return cmd
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
			fmt.Fprintln(w, "PROVIDER\tACCOUNT\tDIR")
			providers := make([]string, 0, len(c.Providers))
			for name := range c.Providers {
				providers = append(providers, name)
			}
			sort.Strings(providers)
			for _, pn := range providers {
				accounts := make([]string, 0, len(c.Providers[pn].Accounts))
				for a := range c.Providers[pn].Accounts {
					accounts = append(accounts, a)
				}
				sort.Strings(accounts)
				for _, a := range accounts {
					fmt.Fprintf(w, "%s\t%s\t%s\n", pn, a, c.Providers[pn].Accounts[a].Dir)
				}
			}
			return w.Flush()
		},
	}
}
