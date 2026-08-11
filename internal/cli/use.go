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
		Use:   "use <provider> <account>",
		Short: "Switch the shell to an account",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
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
	def := "bash"
	if s := os.Getenv("SHELL"); s != "" {
		def = filepath.Base(s)
	}
	cmd.Flags().StringVar(&shellName, "shell", def, "shell dialect for the export line")
	return cmd
}
