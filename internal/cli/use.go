package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/spf13/cobra"
)

func newUseCmd() *cobra.Command {
	var shellName string
	cmd := &cobra.Command{
		Use:   "use [provider] [account]",
		Short: "Switch the shell to an account",
		Long: "Switch the shell to an account.\n\n" +
			"With both provider and account, switches directly. Omit the account " +
			"(or both) to pick one from the interactive TUI.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// No account given: hand off to the interactive picker, scoped to the
			// provider if one was named.
			if len(args) < 2 {
				filter := ""
				if len(args) == 1 {
					filter = args[0]
				}
				return runPicker(cmd, shellName, filter)
			}

			providerName, account := args[0], args[1]
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
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
	cmd.Flags().StringVar(&shellName, "shell", defaultShell(), "shell dialect for the export line")
	return cmd
}

// defaultShell is the current shell's basename, or bash when $SHELL is unset.
func defaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return filepath.Base(s)
	}
	return "bash"
}
