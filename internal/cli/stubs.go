package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func notImplemented(name string) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, _ []string) error {
		fmt.Fprintf(c.OutOrStdout(), "%s: not implemented yet\n", name)
		return nil
	}
}

func newUseCmd() *cobra.Command {
	return &cobra.Command{Use: "use <provider> <account>", Short: "Switch the shell to an account", RunE: notImplemented("use")}
}
func newStatusCmd() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show active account per provider", RunE: notImplemented("status")}
}
func newUsageCmd() *cobra.Command {
	return &cobra.Command{Use: "usage [provider]", Short: "Show token usage per account", RunE: notImplemented("usage")}
}
func newShellInitCmd() *cobra.Command {
	return &cobra.Command{Use: "shell-init <bash|zsh|fish>", Short: "Print the shell hook", RunE: notImplemented("shell-init")}
}
