package cli

import (
	"fmt"

	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/spf13/cobra"
)

func newShellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell-init <bash|zsh|fish>",
		Short: "Print the shell hook",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hook, err := shell.Hook(args[0])
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), hook)
			return nil
		},
	}
}
