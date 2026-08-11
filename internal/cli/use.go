package cli

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/CarlosDanielDev/aiacc/internal/claude"
	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
	"github.com/CarlosDanielDev/aiacc/internal/usage"
	"github.com/spf13/cobra"
)

func newUseCmd() *cobra.Command {
	var shellName string
	cmd := &cobra.Command{
		Use:   "use [provider] [account]",
		Short: "Switch the shell to an account",
		Long: "Switch the shell to an account.\n\n" +
			"With both provider and account, switches directly. Omit the account " +
			"(or both) to pick one from an interactive list.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}

			var providerName, account string
			if len(args) == 2 {
				providerName, account = args[0], args[1]
			} else {
				filter := ""
				if len(args) == 1 {
					filter = args[0]
				}
				rows := collectRows(c, filter)
				if len(rows) == 0 {
					return fmt.Errorf("no accounts registered — run: aiacc add <provider> <account> --dir <path>")
				}
				idx, err := tui.Run(rows)
				if err != nil {
					return err
				}
				if idx < 0 {
					return nil // cancelled — emit nothing, hook eval is a no-op
				}
				providerName, account = rows[idx].Provider, rows[idx].Account
			}

			env, err := provider.EnvVar(c, providerName)
			if err != nil {
				return err
			}
			dir, err := provider.AccountDir(c, providerName, account)
			if err != nil {
				return err
			}
			if _, err := os.Stat(dir); err != nil {
				return fmt.Errorf("account dir does not exist: %s", dir)
			}
			line, err := shell.ExportLine(shellName, env, dir)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), line)
			return nil
		},
	}
	def := "bash"
	if s := os.Getenv("SHELL"); s != "" {
		def = filepath.Base(s)
	}
	cmd.Flags().StringVar(&shellName, "shell", def, "shell dialect for the export line")
	return cmd
}

// collectRows builds the picker rows from config: every account (optionally
// filtered to one provider), with its expanded dir, active flag (live env), and
// token total. Accounts whose dir can't be resolved are skipped.
func collectRows(c *config.Config, filter string) []tui.Row {
	var rows []tui.Row
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		if filter != "" && pn != filter {
			continue
		}
		env, _ := provider.EnvVar(c, pn)
		live := os.Getenv(env)
		p := c.Providers[pn]
		for _, an := range slices.Sorted(maps.Keys(p.Accounts)) {
			dir, err := provider.AccountDir(c, pn, an)
			if err != nil {
				continue
			}
			t, _ := usage.Aggregate(dir)
			rows = append(rows, tui.Row{
				Provider: pn,
				Account:  an,
				Email:    claude.Detect(dir).Email,
				Dir:      dir,
				Active:   env != "" && dir == live,
				Tokens:   t.Total(),
			})
		}
	}
	return rows
}
